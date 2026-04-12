package engine

import (
	"context"
	"sync"
	"time"

	"dojo/internal/workspace"
)

// Expectation tracks the completion state of a single test expectation.
type Expectation struct {
	Target       string
	Index        int // position within the API's ordered expectations
	Fulfilled    bool
	Error        error
	RequiresEval bool
	Deadline     time.Duration
	MaxCalls     int
	MatchCount   int
}

// ActiveTest represents a currently executing test within the Suite.
type ActiveTest struct {
	ID           string
	Test         *workspace.Test
	Suite        *workspace.Suite
	Ctx          context.Context
	Expectations map[string][]*Expectation
	Variables    map[string]any
	APIUsage     map[string]workspace.LLMUsage // Added for LLM usage tracking
	TotalUsage   workspace.LLMUsage            // Added for total LLM usage
	mu           sync.Mutex
	done         chan struct{}
}

// MarkFulfilled marks the expectation at the given index for an API as completed.
// If the expectation is already fulfilled (by a prior match or timeout), the call
// is a no-op to prevent races between match handlers and timeout goroutines.
func (a *ActiveTest) MarkFulfilled(apiName string, idx int, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	exps := a.Expectations[apiName]
	if idx < 0 || idx >= len(exps) {
		return
	}
	exp := exps[idx]
	if exp.Fulfilled {
		return
	}
	
	exp.MatchCount++
	
	targetCalls := exp.MaxCalls
	if targetCalls <= 0 {
		targetCalls = 1
	}

	if exp.MatchCount >= targetCalls {
		exp.Fulfilled = true
	}
	if err != nil {
		exp.Error = err
		exp.Fulfilled = true // error always fulfills
	}

	allDone := true
	for _, slice := range a.Expectations {
		for _, e := range slice {
			if !e.Fulfilled {
				allDone = false
				break
			}
		}
		if !allDone {
			break
		}
	}
	if allDone && a.done != nil {
		select {
		case <-a.done:
		default:
			close(a.done)
		}
	}
}

// FirstUnfulfilled returns the first unfulfilled expectation for the given API,
// or nil if all are fulfilled or the API has no expectations.
func (a *ActiveTest) FirstUnfulfilled(apiName string) *Expectation {
	for _, exp := range a.Expectations[apiName] {
		if !exp.Fulfilled {
			return exp
		}
	}
	return nil
}
