package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/elmacnifico/dojo/internal/workspace"
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

// closeDoneIfAllFulfilledLocked closes the done channel when every expectation is
// fulfilled. The caller must hold a.mu.
func (a *ActiveTest) closeDoneIfAllFulfilledLocked() {
	allDone := true
	for _, slice := range a.Expectations {
		for _, e := range slice {
			if !e.Fulfilled && e.MatchCount == 0 {
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

// ForceFulfillEarly marks the expectation fulfilled without incrementing
// MatchCount. Used when MaxCalls lookahead advances to a later ordered expectation
// because the same request also matches the next spec.
func (a *ActiveTest) ForceFulfillEarly(apiName string, idx int) {
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
	exp.Fulfilled = true
	a.closeDoneIfAllFulfilledLocked()
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

	a.closeDoneIfAllFulfilledLocked()
}

// FirstUnfulfilled returns the first unfulfilled expectation for the given API,
// or nil if all are fulfilled or the API has no expectations. It takes a.mu so
// concurrent MarkFulfilled calls (from proxy goroutines) cannot race with the
// Fulfilled reads.
func (a *ActiveTest) FirstUnfulfilled(apiName string) *Expectation {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, exp := range a.Expectations[apiName] {
		if !exp.Fulfilled {
			return exp
		}
	}
	return nil
}

// buildExpectations populates at.Expectations from parsed Expect lines. Each
// line targets an API (with optional /path suffix stripped for lookup),
// inherits the per-API timeout or the suite expect default, and honors
// Evaluate Response and MaxCalls clauses. Expectations for the same API are
// ordered by declaration. errPrefix labels parse errors (e.g. "test foo" or
// "startup plan").
func buildExpectations(at *ActiveTest, lines []workspace.ParsedLine, test *workspace.Test, suite *workspace.Suite, errPrefix string) error {
	expIdx := make(map[string]int)
	for _, l := range lines {
		apiName := l.Target
		if idx := strings.IndexByte(apiName, '/'); idx >= 0 {
			apiName = apiName[:idx]
		}
		idx := expIdx[apiName]
		exp := &Expectation{
			Target: apiName,
			Index:  idx,
		}
		if d := test.APIs[apiName].TimeoutDuration(); d > 0 {
			exp.Deadline = d
		} else {
			exp.Deadline = suite.Config.Timeouts.Expect.Duration
		}
		for _, clause := range l.Clauses {
			if strings.ToLower(clause.Key) == "evaluate response" {
				exp.RequiresEval = true
			}
		}
		mc, mcHas, mcErr := workspace.ParseMaxCallsFromExpectLine(l)
		if mcErr != nil {
			return fmt.Errorf("%s expect %s: %w", errPrefix, l.Target, mcErr)
		}
		if mcHas {
			exp.MaxCalls = mc
		}
		at.Expectations[apiName] = append(at.Expectations[apiName], exp)
		expIdx[apiName] = idx + 1
	}
	if len(at.Expectations) == 0 {
		close(at.done)
	}
	return nil
}
