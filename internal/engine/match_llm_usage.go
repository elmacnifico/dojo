package engine

import (
	"bytes"
	"encoding/json"

	"dojo/internal/workspace"
)

func intFromAny(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case json.Number:
		i, err := x.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	case int:
		return x, true
	case int64:
		return int(x), true
	default:
		return 0, false
	}
}

func usageMapHasKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

func parseGeminiUsageMetadata(um map[string]any, usage *workspace.LLMUsage) bool {
	found := false
	if v, ok := intFromAny(um["promptTokenCount"]); ok {
		usage.PromptTokens = v
		found = true
	}
	if v, ok := intFromAny(um["prompt_token_count"]); ok && usage.PromptTokens == 0 {
		usage.PromptTokens = v
		found = true
	}
	if v, ok := intFromAny(um["cachedContentTokenCount"]); ok {
		usage.CachedPromptTokens = v
		found = true
	}
	if usage.CachedPromptTokens == 0 {
		for _, key := range []string{
			"cached_content_token_count",
			"cachedContentTokens",
			"cached_prompt_token_count",
		} {
			if v, ok := intFromAny(um[key]); ok {
				usage.CachedPromptTokens = v
				found = true
				break
			}
		}
	}
	if usage.CachedPromptTokens == 0 {
		if pmd, ok := um["promptTokensDetails"].(map[string]any); ok {
			if v, ok := intFromAny(pmd["cachedTokens"]); ok {
				usage.CachedPromptTokens = v
				found = true
			}
		}
	}
	if v, ok := intFromAny(um["candidatesTokenCount"]); ok {
		usage.CompletionTokens = v
		found = true
	}
	if usage.CompletionTokens == 0 {
		for _, key := range []string{"candidates_token_count", "outputTokenCount", "output_token_count"} {
			if v, ok := intFromAny(um[key]); ok {
				usage.CompletionTokens = v
				found = true
				break
			}
		}
	}
	if v, ok := intFromAny(um["toolUsePromptTokenCount"]); ok {
		usage.ToolUsePromptTokens = v
		found = true
	}
	if v, ok := intFromAny(um["thoughtsTokenCount"]); ok {
		usage.ThoughtsTokens = v
		found = true
	}
	if usage.ThoughtsTokens == 0 {
		if v, ok := intFromAny(um["thoughts_token_count"]); ok {
			usage.ThoughtsTokens = v
			found = true
		}
	}
	if v, ok := intFromAny(um["totalTokenCount"]); ok {
		usage.TotalTokens = v
		found = true
	}
	if usage.TotalTokens == 0 && (usage.PromptTokens > 0 || usage.CompletionTokens > 0) {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return found
}

func parseOpenAIChatUsage(u map[string]any, usage *workspace.LLMUsage) bool {
	found := false
	if v, ok := intFromAny(u["prompt_tokens"]); ok {
		usage.PromptTokens = v
		found = true
	}
	if v, ok := intFromAny(u["completion_tokens"]); ok {
		usage.CompletionTokens = v
		found = true
	}
	if v, ok := intFromAny(u["total_tokens"]); ok {
		usage.TotalTokens = v
		found = true
	}
	if pt, ok := u["prompt_tokens_details"].(map[string]any); ok {
		if v, ok := intFromAny(pt["cached_tokens"]); ok {
			usage.CachedPromptTokens = v
			found = true
		}
		if v, ok := intFromAny(pt["audio_tokens"]); ok {
			usage.AudioPromptTokens = v
			found = true
		}
	}
	if ct, ok := u["completion_tokens_details"].(map[string]any); ok {
		if v, ok := intFromAny(ct["reasoning_tokens"]); ok {
			usage.ReasoningTokens = v
			found = true
		}
		if v, ok := intFromAny(ct["audio_tokens"]); ok {
			usage.AudioCompletionTokens = v
			found = true
		}
		if v, ok := intFromAny(ct["accepted_prediction_tokens"]); ok {
			usage.AcceptedPredictionTokens = v
			found = true
		}
		if v, ok := intFromAny(ct["rejected_prediction_tokens"]); ok {
			usage.RejectedPredictionTokens = v
			found = true
		}
	}
	if usage.TotalTokens == 0 && (usage.PromptTokens > 0 || usage.CompletionTokens > 0) {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return found
}

func parseInputOutputUsage(u map[string]any, usage *workspace.LLMUsage) bool {
	if usageMapHasKey(u, "prompt_tokens") {
		return false
	}
	found := false
	if v, ok := intFromAny(u["input_tokens"]); ok {
		usage.PromptTokens = v
		found = true
	}
	if v, ok := intFromAny(u["output_tokens"]); ok {
		usage.CompletionTokens = v
		found = true
	}
	if v, ok := intFromAny(u["cache_creation_input_tokens"]); ok {
		usage.CacheCreationInputTokens = v
		found = true
	}
	if v, ok := intFromAny(u["cache_read_input_tokens"]); ok {
		usage.CacheReadInputTokens = v
		found = true
	}
	if usage.TotalTokens == 0 && found {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return found
}

func parseNestedResponseUsage(data map[string]any, usage *workspace.LLMUsage) bool {
	resp, ok := data["response"].(map[string]any)
	if !ok {
		return false
	}
	u, ok := resp["usage"].(map[string]any)
	if !ok {
		return false
	}
	if usageMapHasKey(u, "prompt_tokens") || usageMapHasKey(u, "completion_tokens") || usageMapHasKey(u, "total_tokens") {
		if parseOpenAIChatUsage(u, usage) {
			return true
		}
	}
	return parseInputOutputUsage(u, usage)
}

// parseUsage extracts LLM token usage from JSON response bodies (OpenAI, Anthropic,
// Gemini, or OpenAI Responses-style). Returns false when no recognized usage block exists.
func parseUsage(payload []byte) (workspace.LLMUsage, bool) {
	var usage workspace.LLMUsage
	var data map[string]any
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		return usage, false
	}

	if um, ok := data["usageMetadata"].(map[string]any); ok {
		if parseGeminiUsageMetadata(um, &usage) {
			return usage, true
		}
	}

	u, ok := data["usage"].(map[string]any)
	if ok {
		if usageMapHasKey(u, "prompt_tokens") || usageMapHasKey(u, "completion_tokens") || usageMapHasKey(u, "total_tokens") {
			if parseOpenAIChatUsage(u, &usage) {
				return usage, true
			}
		}
		if parseInputOutputUsage(u, &usage) {
			return usage, true
		}
	}

	if parseNestedResponseUsage(data, &usage) {
		return usage, true
	}

	return usage, false
}

func addUsageToActiveTest(activeTest *ActiveTest, apiName string, usage workspace.LLMUsage) {
	if activeTest == nil || !usage.AnyUsage() {
		return
	}
	activeTest.mu.Lock()
	defer activeTest.mu.Unlock()
	if activeTest.APIUsage == nil {
		activeTest.APIUsage = make(map[string]workspace.LLMUsage)
	}
	curr := activeTest.APIUsage[apiName]
	curr.Add(usage)
	activeTest.APIUsage[apiName] = curr
	activeTest.TotalUsage.Add(usage)
}
