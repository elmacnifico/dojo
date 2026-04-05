package engine_test

import (
	"testing"

	"dojo/internal/engine"
	"dojo/internal/workspace"
)

func TestEngineEvaluate(t *testing.T) {
	t.Parallel()

	ws := &workspace.Workspace{
		Suites: map[string]*workspace.Suite{
			"test": {
				Config: workspace.DojoConfig{
					Evaluator: &workspace.EvaluatorConfig{
						Provider: "dummy",
					},
				},
				Tests: map[string]*workspace.Test{
					"test_001": {
						Eval: "Check stuff",
					},
				},
			},
		},
	}

	t.Run("unsupported provider returns error", func(t *testing.T) {
		t.Parallel()
		eng := engine.NewEngine(ws)
		activeTest := &engine.ActiveTest{
			ID:    "test_001",
			Test:  ws.Suites["test"].Tests["test_001"],
			Suite: ws.Suites["test"],
		}
		err := eng.Evaluate(activeTest, []byte(`{"id": 123}`))
		if err == nil {
			t.Error("expected evaluation error due to dummy provider")
		}
	})

	t.Run("nil evaluator config returns error", func(t *testing.T) {
		t.Parallel()
		eng := engine.NewEngine(ws)
		suite := &workspace.Suite{Config: workspace.DojoConfig{}}
		activeTest := &engine.ActiveTest{
			ID:    "test_001",
			Test:  ws.Suites["test"].Tests["test_001"],
			Suite: suite,
		}
		err := eng.Evaluate(activeTest, []byte(`{"id": 123}`))
		if err == nil {
			t.Error("expected evaluator config missing error")
		}
	})

	t.Run("empty eval rule returns error", func(t *testing.T) {
		t.Parallel()
		eng := engine.NewEngine(ws)
		activeTest := &engine.ActiveTest{
			ID:    "test_001",
			Test:  &workspace.Test{Eval: ""},
			Suite: ws.Suites["test"],
		}
		err := eng.Evaluate(activeTest, []byte(`{"id": 123}`))
		if err == nil {
			t.Error("expected no eval.md rule error")
		}
	})
}
