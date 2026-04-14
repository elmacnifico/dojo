package main

import (
	"dojo/internal/workspace"
	"testing"
)

func TestLLMCostColumns(t *testing.T) {
	// OpenAI-style: reasoning nested in completion; ThoughtsTokens must be 0 so we subtract.
	u := &workspace.LLMUsage{
		PromptTokens:         100,
		CachedPromptTokens:   20,
		CacheReadInputTokens: 10,
		CompletionTokens:     50,
		ReasoningTokens:      5,
	}
	in, ca, out, th := llmCostColumns(u)
	if in != 70 {
		t.Errorf("input: got %d want 70", in)
	}
	if ca != 30 {
		t.Errorf("cached: got %d want 30", ca)
	}
	if out != 45 {
		t.Errorf("output: got %d want 45", out)
	}
	if th != 5 {
		t.Errorf("thinking: got %d want 5", th)
	}
}

func TestLLMCostColumns_geminiThinkingDisjoint(t *testing.T) {
	u := &workspace.LLMUsage{
		PromptTokens:     2478,
		CompletionTokens: 0,
		ThoughtsTokens:   383,
	}
	_, _, out, th := llmCostColumns(u)
	if th != 383 {
		t.Errorf("thinking: got %d want 383", th)
	}
	if out != 383 {
		t.Errorf("output (candidates+thinking): got %d want 383", out)
	}
}

func TestLLMCostColumns_outputNonNegative(t *testing.T) {
	u := &workspace.LLMUsage{
		PromptTokens:     10,
		CompletionTokens: 3,
		ReasoningTokens:  10,
	}
	_, _, out, th := llmCostColumns(u)
	if out != 3 {
		t.Errorf("disjoint small completion: got %d want 3", out)
	}
	if th != 10 {
		t.Errorf("thinking: got %d want 10", th)
	}
}

func TestLLMCostColumns_inputNonNegative(t *testing.T) {
	u := &workspace.LLMUsage{
		PromptTokens:         10,
		CachedPromptTokens:   8,
		CacheReadInputTokens: 5,
	}
	in, _, _, _ := llmCostColumns(u)
	if in != 0 {
		t.Errorf("input clamp: got %d want 0", in)
	}
}
