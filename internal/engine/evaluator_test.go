package engine_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/elmacnifico/dojo/internal/engine"
	"github.com/elmacnifico/dojo/internal/workspace"
)

func TestAIEvaluator(t *testing.T) {
	promptBytes, err := os.ReadFile("../../eval.md")
	if err != nil {
		t.Fatalf("Failed to read eval.md: %v", err)
	}

	tests := []struct {
		name          string
		provider      string
		mockResp      string
		expectedPass  bool
		expectedReason string
		expectErr     bool
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
			expectedPass:  true,
			expectedReason: "matches perfectly",
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
			expectedPass:  false,
			expectedReason: "missing field 'id'",
		},
		{
			name:      "Invalid JSON response Anthropic",
			provider:  "anthropic",
			mockResp:  `{
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

			evaluator, err := engine.NewAIEvaluator(cfg, string(promptBytes))
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

	evaluator, err := engine.NewAIEvaluator(cfg, string(promptBytes))
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
