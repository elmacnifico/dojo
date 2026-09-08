package engine

import (
	"testing"

	"github.com/elmacnifico/dojo/internal/workspace"
)

// TestEngineEvaluateReusesEvaluator asserts the engine constructs the AI
// evaluator (and its parsed prompt template + HTTP client) at most once and
// reuses it across evaluations, instead of re-parsing the template for every
// Evaluate Response clause.
func TestEngineEvaluateReusesEvaluator(t *testing.T) {
	t.Parallel()
	cfg := &workspace.EvaluatorConfig{Provider: "gemini", Model: "gemini-3.6-flash", APIKeyEnv: "GEMINI_API_KEY"}
	suite := &workspace.Suite{
		Config: workspace.DojoConfig{Evaluator: cfg},
	}
	e := NewEngine(&workspace.Workspace{})
	e.ActiveSuite = suite

	at := &ActiveTest{
		ID:    "t1",
		Suite: suite,
		Test: &workspace.Test{
			APIs: map[string]workspace.APIConfig{},
			Eval: "The payload must contain a valid JSON object.",
		},
		Ctx: nil,
	}

	// First Evaluate call builds and caches the evaluator (it will fail on
	// the missing API key before any HTTP call, which is fine: construction
	// happens before the request).
	_ = e.Evaluate(at, []byte(`{}`))
	first := e.evaluator
	if first == nil {
		t.Fatal("expected evaluator to be constructed after first Evaluate call")
	}

	// Second call must reuse the same instance.
	_ = e.Evaluate(at, []byte(`{}`))
	if e.evaluator != first {
		t.Error("expected engine to reuse the cached evaluator across Evaluate calls")
	}
}
