package workspace

import (
	"math"
	"testing"
)

func TestLLMUsage_Add(t *testing.T) {
	t.Parallel()
	var a LLMUsage
	a.Add(LLMUsage{
		PromptTokens:               10,
		CompletionTokens:           5,
		TotalTokens:                15,
		CachedPromptTokens:         3,
		CacheReadInputTokens:       1,
		CacheCreationInputTokens:   2,
		ReasoningTokens:            4,
		ThoughtsTokens:             6,
		ToolUsePromptTokens:        7,
		AudioPromptTokens:          1,
		AudioCompletionTokens:      2,
		AcceptedPredictionTokens:   8,
		RejectedPredictionTokens:   9,
	})
	a.Add(LLMUsage{PromptTokens: 1, CachedPromptTokens: 2})
	if a.PromptTokens != 11 || a.CachedPromptTokens != 5 {
		t.Fatalf("Add merge: got %+v", a)
	}
}

func TestComputeLLMUsageDerived(t *testing.T) {
	t.Parallel()
	u := LLMUsage{PromptTokens: 100, CachedPromptTokens: 25, CacheReadInputTokens: 10}
	d := ComputeLLMUsageDerived(u)
	if d == nil {
		t.Fatal("expected derived")
	}
	if d.PromptCacheHitRate == nil || math.Abs(*d.PromptCacheHitRate-0.25) > 1e-9 {
		t.Fatalf("prompt cache rate: %v", d.PromptCacheHitRate)
	}
	if d.CacheReadInputRate == nil || math.Abs(*d.CacheReadInputRate-0.10) > 1e-9 {
		t.Fatalf("cache read rate: %v", d.CacheReadInputRate)
	}
	if d.PromptCacheHitNumerator != 25 || d.PromptCacheHitDenominator != 100 {
		t.Fatalf("numerators: %+v", d)
	}
}

func TestComputeLLMUsageDerived_zeroNumerators(t *testing.T) {
	t.Parallel()
	u := LLMUsage{PromptTokens: 50}
	if d := ComputeLLMUsageDerived(u); d != nil {
		t.Fatalf("expected nil when no cache fields, got %+v", d)
	}
}

func TestAggregateLLMUsageFromResults(t *testing.T) {
	t.Parallel()
	results := []TestResult{
		{
			LLMUsage: &LLMUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, CachedPromptTokens: 2},
			LLMUsageByAPI: map[string]LLMUsage{
				"api_a": {PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			},
		},
		{
			LLMUsage: &LLMUsage{PromptTokens: 3, CompletionTokens: 1, TotalTokens: 4},
			LLMUsageByAPI: map[string]LLMUsage{
				"api_a": {PromptTokens: 1, CompletionTokens: 0, TotalTokens: 1},
				"api_b": {PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3},
			},
		},
	}
	total, by := AggregateLLMUsageFromResults(results)
	if total.PromptTokens != 13 || total.CompletionTokens != 6 || total.TotalTokens != 19 || total.CachedPromptTokens != 2 {
		t.Fatalf("total: %+v", total)
	}
	if by == nil {
		t.Fatal("expected byAPI map")
	}
	if by["api_a"].PromptTokens != 11 || by["api_b"].PromptTokens != 2 {
		t.Fatalf("byAPI: %+v", by)
	}
}
