package engine

import (
	"testing"

	"github.com/elmacnifico/dojo/internal/workspace"

	"github.com/jackc/pgproto3/v2"
)

func TestPgResponseCheck_ReadyForQuery(t *testing.T) {
	t.Parallel()
	rfq, _ := (&pgproto3.ReadyForQuery{TxStatus: 'I'}).Encode(nil)

	complete, errMsg := pgResponseCheck(rfq)
	if !complete {
		t.Fatal("expected complete=true for ReadyForQuery")
	}
	if errMsg != "" {
		t.Fatalf("expected empty errMsg, got %q", errMsg)
	}
}

func TestPgResponseCheck_ErrorResponse(t *testing.T) {
	t.Parallel()
	er := &pgproto3.ErrorResponse{
		Severity: "ERROR",
		Message:  "relation \"bogus\" does not exist",
	}
	data, _ := er.Encode(nil)

	complete, errMsg := pgResponseCheck(data)
	if !complete {
		t.Fatal("expected complete=true for ErrorResponse")
	}
	if errMsg == "" {
		t.Fatal("expected non-empty errMsg for ErrorResponse")
	}
	if errMsg != "relation \"bogus\" does not exist" {
		t.Fatalf("unexpected errMsg: %q", errMsg)
	}
}

func TestPgResponseCheck_Incomplete(t *testing.T) {
	t.Parallel()
	complete, errMsg := pgResponseCheck([]byte{})
	if complete {
		t.Fatal("expected complete=false for empty input")
	}
	if errMsg != "" {
		t.Fatalf("expected empty errMsg, got %q", errMsg)
	}
}

func TestPgResponseCheck_CommandCompleteThenReadyForQuery(t *testing.T) {
	t.Parallel()
	cc, _ := (&pgproto3.CommandComplete{CommandTag: []byte("INSERT 0 1")}).Encode(nil)
	rfq, _ := (&pgproto3.ReadyForQuery{TxStatus: 'I'}).Encode(nil)
	data := append(cc, rfq...)

	complete, errMsg := pgResponseCheck(data)
	if !complete {
		t.Fatal("expected complete=true")
	}
	if errMsg != "" {
		t.Fatalf("expected empty errMsg, got %q", errMsg)
	}
}

func TestProcessRequest_TestLevelOverrideWithoutExpect(t *testing.T) {
	t.Parallel()
	eng := &Engine{
		Registry: NewRegistry(),
	}
	eng.ActiveSuite = &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"download": {
				Mode: "mock",
				DefaultResponse: &workspace.DefaultResponse{
					Code:    200,
					Payload: []byte(`suite-level`),
				},
			},
		},
	}
	at := &ActiveTest{
		ID: "t1",
		Test: &workspace.Test{
			APIs: map[string]workspace.APIConfig{
				"download": {
					Mode: "mock",
					DefaultResponse: &workspace.DefaultResponse{
						Code:        200,
						ContentType: "image/jpeg",
						Payload:     []byte(`test-level-binary`),
					},
				},
			},
		},
		Expectations: map[string][]*Expectation{},
		done:         make(chan struct{}),
	}
	eng.Registry.Register("t1", at)

	result := eng.ProcessRequest("http", "download", []byte(`anything`), nil, "")
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if !result.IsMock {
		t.Fatal("expected IsMock=true")
	}
	if result.MockContentType != "image/jpeg" {
		t.Fatalf("expected content type image/jpeg, got %q", result.MockContentType)
	}
	if string(result.MockResponse) != "test-level-binary" {
		t.Fatalf("expected test-level-binary response, got %q", string(result.MockResponse))
	}
}

func TestSplitPhases_SinglePhase(t *testing.T) {
	t.Parallel()
	lines := []workspace.ParsedLine{
		{Action: "Perform", Target: "entrypoints/webhook"},
		{Action: "Expect", Target: "gemini"},
		{Action: "Expect", Target: "postgres"},
	}
	phases := workspace.SplitPlanPhases(lines)
	if len(phases) != 1 {
		t.Fatalf("expected 1 phase, got %d", len(phases))
	}
	if len(phases[0].Expects) != 2 {
		t.Fatalf("expected 2 expects in phase 0, got %d", len(phases[0].Expects))
	}
}

func TestSplitPhases_TwoPhases(t *testing.T) {
	t.Parallel()
	lines := []workspace.ParsedLine{
		{Action: "Perform", Target: "entrypoints/webhook"},
		{Action: "Expect", Target: "gemini"},
		{Action: "Expect", Target: "postgres"},
		{Action: "Perform", Target: "postgres"},
	}
	phases := workspace.SplitPlanPhases(lines)
	if len(phases) != 2 {
		t.Fatalf("expected 2 phases, got %d", len(phases))
	}
	if len(phases[0].Expects) != 2 {
		t.Fatalf("expected 2 expects in phase 0, got %d", len(phases[0].Expects))
	}
	if len(phases[1].Expects) != 0 {
		t.Fatalf("expected 0 expects in phase 1, got %d", len(phases[1].Expects))
	}
	if phases[1].Perform.Target != "postgres" {
		t.Fatalf("expected phase 1 target = postgres, got %q", phases[1].Perform.Target)
	}
}

func TestParseUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		payload   []byte
		want      workspace.LLMUsage
		wantFound bool
	}{
		{
			name:      "OpenAI format",
			payload:   []byte(`{"usage": {"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30}}`),
			want:      workspace.LLMUsage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
			wantFound: true,
		},
		{
			name:      "Gemini format",
			payload:   []byte(`{"usageMetadata": {"promptTokenCount": 15, "candidatesTokenCount": 25, "totalTokenCount": 40}}`),
			want:      workspace.LLMUsage{PromptTokens: 15, CompletionTokens: 25, TotalTokens: 40},
			wantFound: true,
		},
		{
			name:    "OpenAI with details",
			payload: []byte(`{"usage": {"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120, "prompt_tokens_details": {"cached_tokens": 40, "audio_tokens": 3}, "completion_tokens_details": {"reasoning_tokens": 5, "audio_tokens": 2, "accepted_prediction_tokens": 1, "rejected_prediction_tokens": 2}}}`),
			want: workspace.LLMUsage{
				PromptTokens:               100,
				CompletionTokens:           20,
				TotalTokens:                120,
				CachedPromptTokens:         40,
				AudioPromptTokens:          3,
				ReasoningTokens:            5,
				AudioCompletionTokens:      2,
				AcceptedPredictionTokens:   1,
				RejectedPredictionTokens:   2,
			},
			wantFound: true,
		},
		{
			name:      "OpenAI Responses-style usage",
			payload:   []byte(`{"usage": {"input_tokens": 50, "output_tokens": 12}}`),
			want:      workspace.LLMUsage{PromptTokens: 50, CompletionTokens: 12, TotalTokens: 62},
			wantFound: true,
		},
		{
			name:    "Anthropic with cache",
			payload: []byte(`{"usage": {"input_tokens": 200, "output_tokens": 30, "cache_creation_input_tokens": 100, "cache_read_input_tokens": 50}}`),
			want: workspace.LLMUsage{
				PromptTokens:               200,
				CompletionTokens:           30,
				TotalTokens:                230,
				CacheCreationInputTokens:   100,
				CacheReadInputTokens:       50,
			},
			wantFound: true,
		},
		{
			name:    "Gemini extended",
			payload: []byte(`{"usageMetadata": {"promptTokenCount": 80, "cachedContentTokenCount": 32, "candidatesTokenCount": 10, "toolUsePromptTokenCount": 7, "thoughtsTokenCount": 3, "totalTokenCount": 100}}`),
			want: workspace.LLMUsage{
				PromptTokens:          80,
				CompletionTokens:      10,
				TotalTokens:           100,
				CachedPromptTokens:    32,
				ToolUsePromptTokens:   7,
				ThoughtsTokens:        3,
			},
			wantFound: true,
		},
		{
			name:    "Gemini snake_case usageMetadata",
			payload: []byte(`{"usageMetadata": {"prompt_token_count": 80, "cached_content_token_count": 25, "candidates_token_count": 12, "thoughts_token_count": 4}}`),
			want: workspace.LLMUsage{
				PromptTokens:       80,
				CompletionTokens:   12,
				CachedPromptTokens: 25,
				ThoughtsTokens:     4,
				TotalTokens:        92,
			},
			wantFound: true,
		},
		{
			name:      "OpenAI prefers prompt_tokens over input_tokens",
			payload:   []byte(`{"usage": {"prompt_tokens": 1, "input_tokens": 999}}`),
			want:      workspace.LLMUsage{PromptTokens: 1, TotalTokens: 1},
			wantFound: true,
		},
		{
			name:      "Nested response.usage OpenAI",
			payload:   []byte(`{"response": {"usage": {"prompt_tokens": 5, "completion_tokens": 6, "total_tokens": 11}}}`),
			want:      workspace.LLMUsage{PromptTokens: 5, CompletionTokens: 6, TotalTokens: 11},
			wantFound: true,
		},
		{
			name:      "No usage data",
			payload:   []byte(`{"choices": [{"text": "hello"}]}`),
			want:      workspace.LLMUsage{},
			wantFound: false,
		},
		{
			name:      "Invalid JSON",
			payload:   []byte(`{invalid`),
			want:      workspace.LLMUsage{},
			wantFound: false,
		},
		{
			name:      "Partial OpenAI format",
			payload:   []byte(`{"usage": {"prompt_tokens": 10}}`),
			want:      workspace.LLMUsage{PromptTokens: 10, TotalTokens: 10},
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := parseUsage(tt.payload)
			if found != tt.wantFound {
				t.Errorf("parseUsage() found = %v, want %v", found, tt.wantFound)
			}
			if got != tt.want {
				t.Errorf("parseUsage() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestActiveTest_MarkFulfilled_MaxCalls(t *testing.T) {
	t.Parallel()

	at := &ActiveTest{
		Expectations: map[string][]*Expectation{
			"api1": {
				{Target: "api1", MaxCalls: 3},
			},
		},
		done: make(chan struct{}),
	}

	// First call
	at.MarkFulfilled("api1", 0, nil)
	exp := at.Expectations["api1"][0]
	if exp.MatchCount != 1 {
		t.Errorf("expected MatchCount 1, got %d", exp.MatchCount)
	}
	if exp.Fulfilled {
		t.Error("expected Fulfilled false after 1 call")
	}

	// Second call
	at.MarkFulfilled("api1", 0, nil)
	if exp.MatchCount != 2 {
		t.Errorf("expected MatchCount 2, got %d", exp.MatchCount)
	}
	if exp.Fulfilled {
		t.Error("expected Fulfilled false after 2 calls")
	}

	// Third call
	at.MarkFulfilled("api1", 0, nil)
	if exp.MatchCount != 3 {
		t.Errorf("expected MatchCount 3, got %d", exp.MatchCount)
	}
	if !exp.Fulfilled {
		t.Error("expected Fulfilled true after 3 calls")
	}

	// Fourth call (should be no-op because it's fulfilled)
	at.MarkFulfilled("api1", 0, nil)
	if exp.MatchCount != 3 {
		t.Errorf("expected MatchCount 3 after fulfilled, got %d", exp.MatchCount)
	}
}
