package engine_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elmacnifico/dojo/internal/engine"
	"github.com/elmacnifico/dojo/internal/workspace"
)

func TestAIEvaluator(t *testing.T) {
	promptBytes, err := os.ReadFile("../../eval.md")
	if err != nil {
		t.Fatalf("Failed to read eval.md: %v", err)
	}

	tests := []struct {
		name           string
		provider       string
		mockResp       string
		expectedPass   bool
		expectedReason string
		expectErr      bool
	}{
		{
			name:     "Valid JSON passing OpenAI",
			provider: "openai",
			mockResp: `{
				"choices": [{
					"message": {
						"content": "{\"pass\": true, \"reason\": \"matches perfectly\"}"
					}
				}]
			}`,
			expectedPass:   true,
			expectedReason: "matches perfectly",
		},
		{
			name:     "Valid JSON passing Melious",
			provider: "melious",
			mockResp: `{
				"choices": [{
					"message": {
						"content": "{\"pass\": true, \"reason\": \"sovereign match\"}"
					}
				}]
			}`,
			expectedPass:   true,
			expectedReason: "sovereign match",
		},
		{
			name:     "Valid JSON failing Gemini",
			provider: "gemini",
			mockResp: `{
				"candidates": [{
					"content": {
						"parts": [{
							"text": "` + "```json\\n{\\\"pass\\\": false, \\\"reason\\\": \\\"missing field 'id'\\\"}\\n```" + `"
						}]
					}
				}]
			}`,
			expectedPass:   false,
			expectedReason: "missing field 'id'",
		},
		{
			name:     "Invalid JSON response Anthropic",
			provider: "anthropic",
			mockResp: `{
				"content": [{
					"text": "This looks fine to me, but I didn't format it as JSON."
				}]
			}`,
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintln(w, tc.mockResp)
			}))
			defer ts.Close()

			t.Setenv("TEST_API_KEY", "secret")

			cfg := &workspace.EvaluatorConfig{
				Provider:  tc.provider,
				Model:     "test-model",
				APIKeyEnv: "TEST_API_KEY",
				URL:       ts.URL,
			}

			evaluator, err := engine.NewAIEvaluator(cfg, string(promptBytes), 0)
			if err != nil {
				t.Fatalf("Failed to create evaluator: %v", err)
			}

			result, err := evaluator.Evaluate(context.Background(), []byte(`{"id": 123}`), "must contain an ID field")

			if tc.expectErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if result.Pass != tc.expectedPass {
				t.Errorf("Expected Pass=%v, got %v", tc.expectedPass, result.Pass)
			}

			if result.Reason != tc.expectedReason {
				t.Errorf("Expected Reason=%q, got %q", tc.expectedReason, result.Reason)
			}
		})
	}
}

func TestAIEvaluator_openaiUpstreamHTTPError(t *testing.T) {
	promptBytes, err := os.ReadFile("../../eval.md")
	if err != nil {
		t.Fatalf("read eval.md: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		if _, err := w.Write([]byte("denied")); err != nil {
			t.Errorf("write error body: %v", err)
		}
	}))
	defer ts.Close()

	t.Setenv("TEST_API_KEY", "secret")

	cfg := &workspace.EvaluatorConfig{
		Provider:  "openai",
		Model:     "test-model",
		APIKeyEnv: "TEST_API_KEY",
		URL:       ts.URL,
	}

	evaluator, err := engine.NewAIEvaluator(cfg, string(promptBytes), 0)
	if err != nil {
		t.Fatalf("NewAIEvaluator: %v", err)
	}

	_, err = evaluator.Evaluate(context.Background(), []byte(`{"id": 1}`), "rule")
	if err == nil {
		t.Fatal("expected error from upstream HTTP 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error should mention status, got: %v", err)
	}
}

// TestAIEvaluator_retriesTransientFailures verifies that the evaluator
// retries transient provider failures (transport resets, 429, 5xx) and
// succeeds once a healthy attempt lands, instead of failing the test on
// the first hiccup.
func TestAIEvaluator_retriesTransientFailures(t *testing.T) {
	promptBytes, err := os.ReadFile("../../eval.md")
	if err != nil {
		t.Fatalf("read eval.md: %v", err)
	}

	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		switch n {
		case 1: // transport-level failure: close mid-request
			panic(http.ErrAbortHandler)
		case 2: // rate limited
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintln(w, `{"error":"rate limited"}`)
		default: // healthy attempt
			fmt.Fprintln(w, `{"choices":[{"message":{"content":"{\"pass\": true, \"reason\": \"ok\"}"}}]}`)
		}
	}))
	defer ts.Close()

	t.Setenv("TEST_API_KEY", "secret")

	cfg := &workspace.EvaluatorConfig{
		Provider:  "openai",
		Model:     "test-model",
		APIKeyEnv: "TEST_API_KEY",
		URL:       ts.URL,
	}

	evaluator, err := engine.NewAIEvaluator(cfg, string(promptBytes), 5*time.Second)
	if err != nil {
		t.Fatalf("NewAIEvaluator: %v", err)
	}

	result, err := evaluator.Evaluate(context.Background(), []byte(`{"id": 1}`), "rule")
	if err != nil {
		t.Fatalf("expected the evaluator to recover after transient failures, got: %v", err)
	}
	if !result.Pass {
		t.Fatalf("expected pass result, got: %+v", result)
	}
	if got := attempts.Load(); got < 3 {
		t.Fatalf("expected at least 3 attempts (2 transient failures + 1 success), got %d", got)
	}
}

// TestAIEvaluator_doesNotRetryDeterministic4xx verifies that a 401 fails
// on the first attempt — deterministic client errors must not be retried.
func TestAIEvaluator_doesNotRetryDeterministic4xx(t *testing.T) {
	promptBytes, err := os.ReadFile("../../eval.md")
	if err != nil {
		t.Fatalf("read eval.md: %v", err)
	}

	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintln(w, "denied")
	}))
	defer ts.Close()

	t.Setenv("TEST_API_KEY", "secret")

	cfg := &workspace.EvaluatorConfig{
		Provider:  "openai",
		Model:     "test-model",
		APIKeyEnv: "TEST_API_KEY",
		URL:       ts.URL,
	}

	evaluator, err := engine.NewAIEvaluator(cfg, string(promptBytes), 5*time.Second)
	if err != nil {
		t.Fatalf("NewAIEvaluator: %v", err)
	}

	_, err = evaluator.Evaluate(context.Background(), []byte(`{"id": 1}`), "rule")
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("deterministic 4xx must not be retried, got %d attempts", got)
	}
}
