package workspace

import "math"

// Add accumulates token counters from b into u (used when aggregating per-call usage).
func (u *LLMUsage) Add(b LLMUsage) {
	u.PromptTokens += b.PromptTokens
	u.CompletionTokens += b.CompletionTokens
	u.TotalTokens += b.TotalTokens
	u.CachedPromptTokens += b.CachedPromptTokens
	u.CacheReadInputTokens += b.CacheReadInputTokens
	u.CacheCreationInputTokens += b.CacheCreationInputTokens
	u.ReasoningTokens += b.ReasoningTokens
	u.AudioPromptTokens += b.AudioPromptTokens
	u.AudioCompletionTokens += b.AudioCompletionTokens
	u.AcceptedPredictionTokens += b.AcceptedPredictionTokens
	u.RejectedPredictionTokens += b.RejectedPredictionTokens
	u.ThoughtsTokens += b.ThoughtsTokens
	u.ToolUsePromptTokens += b.ToolUsePromptTokens
}

// AnyUsage reports whether any usage counter is non-zero.
func (u LLMUsage) AnyUsage() bool {
	return u.PromptTokens != 0 ||
		u.CompletionTokens != 0 ||
		u.TotalTokens != 0 ||
		u.CachedPromptTokens != 0 ||
		u.CacheReadInputTokens != 0 ||
		u.CacheCreationInputTokens != 0 ||
		u.ReasoningTokens != 0 ||
		u.AudioPromptTokens != 0 ||
		u.AudioCompletionTokens != 0 ||
		u.AcceptedPredictionTokens != 0 ||
		u.RejectedPredictionTokens != 0 ||
		u.ThoughtsTokens != 0 ||
		u.ToolUsePromptTokens != 0
}

// LLMUsageDerived holds rates derived from aggregated [LLMUsage] (not summed across calls).
type LLMUsageDerived struct {
	PromptCacheHitRate *float64 `json:"prompt_cache_hit_rate,omitempty" yaml:"prompt_cache_hit_rate,omitempty"`
	CacheReadInputRate *float64 `json:"cache_read_input_rate,omitempty" yaml:"cache_read_input_rate,omitempty"`

	PromptCacheHitNumerator   int `json:"prompt_cache_hit_numerator,omitempty" yaml:"prompt_cache_hit_numerator,omitempty"`
	PromptCacheHitDenominator int `json:"prompt_cache_hit_denominator,omitempty" yaml:"prompt_cache_hit_denominator,omitempty"`
	CacheReadInputNumerator   int `json:"cache_read_input_numerator,omitempty" yaml:"cache_read_input_numerator,omitempty"`
	CacheReadInputDenominator int `json:"cache_read_input_denominator,omitempty" yaml:"cache_read_input_denominator,omitempty"`
}

// ComputeLLMUsageDerived builds derived ratios from aggregated usage totals.
// Prompt cache hit rate uses CachedPromptTokens (OpenAI prompt cache + Gemini cached content).
// Cache read input rate uses CacheReadInputTokens (Anthropic cache reads) over PromptTokens.
func ComputeLLMUsageDerived(u LLMUsage) *LLMUsageDerived {
	d := &LLMUsageDerived{}
	if u.PromptTokens > 0 && u.CachedPromptTokens > 0 {
		r := float64(u.CachedPromptTokens) / float64(u.PromptTokens)
		if !math.IsNaN(r) && !math.IsInf(r, 0) {
			d.PromptCacheHitRate = &r
			d.PromptCacheHitNumerator = u.CachedPromptTokens
			d.PromptCacheHitDenominator = u.PromptTokens
		}
	}
	if u.PromptTokens > 0 && u.CacheReadInputTokens > 0 {
		r := float64(u.CacheReadInputTokens) / float64(u.PromptTokens)
		if !math.IsNaN(r) && !math.IsInf(r, 0) {
			d.CacheReadInputRate = &r
			d.CacheReadInputNumerator = u.CacheReadInputTokens
			d.CacheReadInputDenominator = u.PromptTokens
		}
	}
	if d.PromptCacheHitRate == nil && d.CacheReadInputRate == nil {
		return nil
	}
	return d
}

// AggregateLLMUsageFromResults sums LLM counters across test results: each test's
// llm_usage totals plus every llm_usage_by_api entry (same API name summed across tests).
func AggregateLLMUsageFromResults(results []TestResult) (LLMUsage, map[string]LLMUsage) {
	var total LLMUsage
	byAPI := make(map[string]LLMUsage)
	for _, r := range results {
		if r.LLMUsage != nil {
			total.Add(*r.LLMUsage)
		}
		for name, u := range r.LLMUsageByAPI {
			cur := byAPI[name]
			cur.Add(u)
			byAPI[name] = cur
		}
	}
	if len(byAPI) == 0 {
		return total, nil
	}
	// Drop empty API buckets after aggregation.
	for k, v := range byAPI {
		if !v.AnyUsage() {
			delete(byAPI, k)
		}
	}
	if len(byAPI) == 0 {
		return total, nil
	}
	return total, byAPI
}
