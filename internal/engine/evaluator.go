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

	"dojo/internal/workspace"
	"dojo/pkg/dojo"
)

// AIEvaluator implements dojo.Evaluator using generative AI.
type AIEvaluator struct {
	config *workspace.EvaluatorConfig
	tmpl   *template.Template
	client *http.Client
}

type evalData struct {
	ExpectedRule  string
	ActualPayload string
}

// NewAIEvaluator initializes an AIEvaluator.
func NewAIEvaluator(config *workspace.EvaluatorConfig, promptTemplate string) (*AIEvaluator, error) {
	if config == nil {
		return nil, fmt.Errorf("evaluator config cannot be nil")
	}
	tmpl, err := template.New("eval").Parse(promptTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse prompt template: %w", err)
	}

	return &AIEvaluator{
		config: config,
		tmpl:   tmpl,
		client: &http.Client{},
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

	switch strings.ToLower(a.config.Provider) {
	case "openai":
		responseText, err = a.callOpenAI(ctx, prompt, apiKey)
	case "anthropic":
		responseText, err = a.callAnthropic(ctx, prompt, apiKey)
	case "gemini":
		responseText, err = a.callGemini(ctx, prompt, apiKey)
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

	var result dojo.EvaluatorResult
	if err := json.Unmarshal([]byte(rawJSON), &result); err != nil {
		return dojo.EvaluatorResult{}, fmt.Errorf("failed to parse ai response as json: %w\nRaw Output: %s", err, rawJSON)
	}

	return result, nil
}

// llmRequest holds the parameters needed to make a provider-specific LLM API call.
type llmRequest struct {
	provider   string
	defaultURL string
	body       map[string]any
	setAuth    func(req *http.Request)
	extractText func(body []byte) (string, error)
}

// doLLMRequest executes a provider-agnostic LLM HTTP call.
func (a *AIEvaluator) doLLMRequest(ctx context.Context, r llmRequest) (string, error) {
	b, err := json.Marshal(r.body)
	if err != nil {
		return "", fmt.Errorf("marshal %s request: %w", r.provider, err)
	}

	reqURL := a.config.URL
	if reqURL == "" {
		reqURL = r.defaultURL
	}

	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("build %s request: %w", r.provider, err)
	}
	req.Header.Set("Content-Type", "application/json")
	r.setAuth(req)

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s request failed: %w", r.provider, err)
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", fmt.Errorf("%s error reading body: %w", r.provider, readErr)
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%s error %d: %s", r.provider, resp.StatusCode, string(respBody))
	}

	return r.extractText(respBody)
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
		reqURL = fmt.Sprintf("%s?key=%s", reqURL, apiKey)
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
