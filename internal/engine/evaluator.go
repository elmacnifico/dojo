package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/elmacnifico/dojo/internal/workspace"
	"github.com/elmacnifico/dojo/pkg/dojo"
)

// evalMaxAttempts is how many times the evaluator retries a transient
// provider failure (transport error, 429, 5xx).
const evalMaxAttempts = 3

// AIEvaluator implements dojo.Evaluator using generative AI.
type AIEvaluator struct {
	config *workspace.EvaluatorConfig
	tmpl   *template.Template
	client *http.Client
	// attemptTimeout bounds each individual provider call (per attempt,
	// not shared across retries). Zero disables per-attempt bounding.
	attemptTimeout time.Duration
}

// evaluatorPromptTemplate is the fixed prompt the engine sends to the
// evaluator model. Kept as a constant so the template is parsed once per
// engine instead of once per Evaluate Response clause.
const evaluatorPromptTemplate = "You are a strict test evaluator. Decide whether the ACTUAL PAYLOAD satisfies every rule in EXPECTED RULE.\n\nEXPECTED RULE:\n{{.ExpectedRule}}\n\nACTUAL PAYLOAD:\n{{.ActualPayload}}\n\nRespond with ONLY a JSON object in this exact format (no markdown, no extra text):\n{\"pass\": true, \"reason\": \"short explanation\"}\nSet \"pass\" to true if ALL rules are satisfied, false otherwise. Always include a \"reason\"."

type evalData struct {
	ExpectedRule  string
	ActualPayload string
}

// NewAIEvaluator initializes an AIEvaluator. attemptTimeout bounds each
// individual provider call (retries get a fresh budget each); zero or a
// negative value disables per-attempt bounding and leaves only the caller's
// context deadline in force.
func NewAIEvaluator(config *workspace.EvaluatorConfig, promptTemplate string, attemptTimeout time.Duration) (*AIEvaluator, error) {
	if config == nil {
		return nil, fmt.Errorf("evaluator config cannot be nil")
	}
	tmpl, err := template.New("eval").Parse(promptTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse prompt template: %w", err)
	}

	return &AIEvaluator{
		config:         config,
		tmpl:           tmpl,
		client:         &http.Client{},
		attemptTimeout: attemptTimeout,
	}, nil
}

// Evaluate evaluates the payload using the specified AI provider.
func (a *AIEvaluator) Evaluate(ctx context.Context, actual []byte, expectedRule string) (dojo.EvaluatorResult, error) {
	var promptBuilder strings.Builder
	err := a.tmpl.Execute(&promptBuilder, evalData{
		ExpectedRule:  expectedRule,
		ActualPayload: string(actual),
	})
	if err != nil {
		return dojo.EvaluatorResult{}, fmt.Errorf("failed to execute template: %w", err)
	}

	prompt := promptBuilder.String()
	apiKey := os.Getenv(a.config.APIKeyEnv)
	if apiKey == "" && a.config.APIKeyEnv != "" {
		return dojo.EvaluatorResult{}, fmt.Errorf("API key environment variable %s is not set", a.config.APIKeyEnv)
	}

	var responseText string

	// Generation + parse loop: retry once when the judge model wraps its
	// JSON in prose (or emits malformed JSON). The next attempt usually
	// produces clean output; a second parse failure is deterministic
	// enough to surface as an error.
	var result dojo.EvaluatorResult
	var lastParseErr error
	for genAttempt := 1; genAttempt <= evalMaxAttempts; genAttempt++ {
	switch strings.ToLower(a.config.Provider) {
	case "openai":
		responseText, err = a.callOpenAI(ctx, prompt, apiKey)
	case "anthropic":
		responseText, err = a.callAnthropic(ctx, prompt, apiKey)
	case "gemini":
		responseText, err = a.callGemini(ctx, prompt, apiKey)
	case "melious":
		responseText, err = a.callMelious(ctx, prompt, apiKey)
	default:
		return dojo.EvaluatorResult{}, fmt.Errorf("unsupported AI provider: %s", a.config.Provider)
	}

		if err != nil {
			return dojo.EvaluatorResult{}, fmt.Errorf("ai generation failed: %w", err)
		}

		rawJSON := strings.TrimSpace(responseText)
		if strings.HasPrefix(rawJSON, "```json") {
			rawJSON = strings.TrimPrefix(rawJSON, "```json")
			rawJSON = strings.TrimSuffix(rawJSON, "```")
		} else if strings.HasPrefix(rawJSON, "```") {
			rawJSON = strings.TrimPrefix(rawJSON, "```")
			rawJSON = strings.TrimSuffix(rawJSON, "```")
		}
		rawJSON = strings.TrimSpace(rawJSON)

		if unmarshalErr := json.Unmarshal([]byte(rawJSON), &result); unmarshalErr == nil {
			return result, nil
		} else {
			lastParseErr = unmarshalErr
			if genAttempt == evalMaxAttempts || ctx.Err() != nil {
				break
			}
			// Brief pause before the retry generation.
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				break
			}
		}
	}

	return dojo.EvaluatorResult{}, fmt.Errorf("failed to parse ai response as json: %w\nRaw Output: %s", lastParseErr, responseText)
}

// llmRequest holds the parameters needed to make a provider-specific LLM API call.
type llmRequest struct {
	provider    string
	defaultURL  string
	body        map[string]any
	setAuth     func(req *http.Request)
	extractText func(body []byte) (string, error)
}

// doLLMRequest executes a provider-agnostic LLM HTTP call with retries.
// Transient failures (transport errors, 429, 5xx) are retried up to
// evalMaxAttempts times with exponential backoff; deterministic failures
// (other 4xx, decode errors) fail immediately. Each attempt gets a fresh
// per-call timeout derived from the parent context so one slow attempt
// does not consume the budget of the retries after it.
func (a *AIEvaluator) doLLMRequest(ctx context.Context, r llmRequest) (string, error) {
	b, err := json.Marshal(r.body)
	if err != nil {
		return "", fmt.Errorf("marshal %s request: %w", r.provider, err)
	}

	reqURL := a.config.URL
	if reqURL == "" {
		reqURL = r.defaultURL
	}

	var lastErr error
	for attempt := 1; attempt <= evalMaxAttempts; attempt++ {
		// Fresh per-attempt timeout: the engine-level deadline (timeouts.
		// ai_evaluator) bounds the whole evaluation, but a single hung
		// attempt must not starve its own retries. The parent context
		// still enforces the hard cap.
		attemptCtx := ctx
		if a.attemptTimeout > 0 {
			var cancel context.CancelFunc
			attemptCtx, cancel = context.WithTimeout(ctx, a.attemptTimeout)
			defer cancel()
		}

		text, retryable, err := a.doLLMAttempt(attemptCtx, reqURL, b, r)
		if err == nil {
			return text, nil
		}
		lastErr = err
		if !retryable || attempt == evalMaxAttempts || ctx.Err() != nil {
			return "", err
		}
		// Exponential backoff: 1s, 2s, ... (attempt 1 waits 1s before
		// attempt 2, attempt 2 waits 2s before attempt 3).
		select {
		case <-time.After(time.Duration(attempt) * time.Second):
		case <-ctx.Done():
			return "", lastErr
		}
	}
	return "", lastErr
}

// doLLMAttempt performs one HTTP attempt. retryable reports whether the
// failure is transient (transport error, 429, 5xx) and worth retrying.
func (a *AIEvaluator) doLLMAttempt(ctx context.Context, reqURL string, body []byte, r llmRequest) (text string, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader(body))
	if err != nil {
		return "", false, fmt.Errorf("build %s request: %w", r.provider, err)
	}
	req.Header.Set("Content-Type", "application/json")
	r.setAuth(req)

	resp, err := a.client.Do(req)
	if err != nil {
		// Transport errors (timeouts, connection resets) are transient.
		return "", true, fmt.Errorf("%s request failed: %w", r.provider, err)
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", true, fmt.Errorf("%s error reading body: %w", r.provider, readErr)
	}

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return "", true, fmt.Errorf("%s error %d: %s", r.provider, resp.StatusCode, string(respBody))
	}
	if resp.StatusCode >= 400 {
		return "", false, fmt.Errorf("%s error %d: %s", r.provider, resp.StatusCode, string(respBody))
	}

	text, err = r.extractText(respBody)
	if err != nil {
		// A malformed success response is deterministic; do not retry.
		return "", false, err
	}
	return text, false, nil
}

func (a *AIEvaluator) callOpenAI(ctx context.Context, prompt, apiKey string) (string, error) {
	return a.doLLMRequest(ctx, llmRequest{
		provider:   "openai",
		defaultURL: "https://api.openai.com/v1/chat/completions",
		body: map[string]any{
			"model": a.config.Model,
			"messages": []map[string]string{
				{"role": "user", "content": prompt},
			},
		},
		setAuth: func(req *http.Request) {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		},
		extractText: func(body []byte) (string, error) {
			var res struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			if err := json.Unmarshal(body, &res); err != nil {
				return "", fmt.Errorf("decode openai response: %w", err)
			}
			if len(res.Choices) == 0 {
				return "", fmt.Errorf("empty response from openai")
			}
			return res.Choices[0].Message.Content, nil
		},
	})
}

// callMelious calls the Melious sovereign AI platform. Melious exposes an
// OpenAI-compatible chat completions API (base URL https://api.melious.ai/v1)
// over 60+ open-weight models hosted on EU infrastructure, so the wire
// format and response shape are identical to OpenAI.
func (a *AIEvaluator) callMelious(ctx context.Context, prompt, apiKey string) (string, error) {
	return a.doLLMRequest(ctx, llmRequest{
		provider:   "melious",
		defaultURL: "https://api.melious.ai/v1/chat/completions",
		body: map[string]any{
			"model": a.config.Model,
			"messages": []map[string]string{
				{"role": "user", "content": prompt},
			},
		},
		setAuth: func(req *http.Request) {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		},
		extractText: func(body []byte) (string, error) {
			var res struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			if err := json.Unmarshal(body, &res); err != nil {
				return "", fmt.Errorf("decode melious response: %w", err)
			}
			if len(res.Choices) == 0 {
				return "", fmt.Errorf("empty response from melious")
			}
			return res.Choices[0].Message.Content, nil
		},
	})
}

func (a *AIEvaluator) callAnthropic(ctx context.Context, prompt, apiKey string) (string, error) {
	return a.doLLMRequest(ctx, llmRequest{
		provider:   "anthropic",
		defaultURL: "https://api.anthropic.com/v1/messages",
		body: map[string]any{
			"model":      a.config.Model,
			"max_tokens": 1024,
			"messages": []map[string]string{
				{"role": "user", "content": prompt},
			},
		},
		setAuth: func(req *http.Request) {
			req.Header.Set("x-api-key", apiKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		},
		extractText: func(body []byte) (string, error) {
			var res struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.Unmarshal(body, &res); err != nil {
				return "", fmt.Errorf("decode anthropic response: %w", err)
			}
			if len(res.Content) == 0 {
				return "", fmt.Errorf("empty response from anthropic")
			}
			return res.Content[0].Text, nil
		},
	})
}

func (a *AIEvaluator) callGemini(ctx context.Context, prompt, apiKey string) (string, error) {
	defaultURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", a.config.Model, apiKey)

	reqURL := a.config.URL
	if reqURL == "" {
		reqURL = defaultURL
	} else if !strings.Contains(reqURL, "?key=") {
		sep := "?"
		if strings.Contains(reqURL, "?") {
			sep = "&"
		}
		reqURL = fmt.Sprintf("%s%skey=%s", reqURL, sep, apiKey)
	}

	return a.doLLMRequest(ctx, llmRequest{
		provider:   "gemini",
		defaultURL: reqURL,
		body: map[string]any{
			"contents": []map[string]any{
				{"parts": []map[string]string{{"text": prompt}}},
			},
		},
		setAuth: func(req *http.Request) {},
		extractText: func(body []byte) (string, error) {
			var res struct {
				Candidates []struct {
					Content struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"content"`
				} `json:"candidates"`
			}
			if err := json.Unmarshal(body, &res); err != nil {
				return "", fmt.Errorf("decode gemini response: %w", err)
			}
			if len(res.Candidates) == 0 || len(res.Candidates[0].Content.Parts) == 0 {
				return "", fmt.Errorf("empty response from gemini")
			}
			return res.Candidates[0].Content.Parts[0].Text, nil
		},
	})
}
