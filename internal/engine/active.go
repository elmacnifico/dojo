package engine

import (
	"context"
	"sync"

	"dojo/internal/workspace"
)

// Expectation tracks the completion state of a test expectation.
type Expectation struct {
	Target       string
	Fulfilled    bool
	Error        error
	RequiresEval bool
}

// ActiveTest represents a currently executing test within the Suite.
type ActiveTest struct {
	ID           string
	Test         *workspace.Test
	Suite        *workspace.Suite
	Ctx          context.Context
	Expectations map[string]*Expectation
	mu           sync.Mutex
	done         chan struct{}
}

// MarkFulfilled marks an expectation for a given API as completed.
func (a *ActiveTest) MarkFulfilled(apiName string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if exp, ok := a.Expectations[apiName]; ok {
		exp.Fulfilled = true
		if exp.Error == nil {
			exp.Error = err
		}
	}

	allDone := true
	for _, exp := range a.Expectations {
		if !exp.Fulfilled {
			allDone = false
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
