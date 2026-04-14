package engine

import (
	"errors"
	"testing"

	"github.com/elmacnifico/dojo/internal/workspace"
)

func TestReconcileSummaryForSeeding_Nil(t *testing.T) {
	t.Parallel()
	reconcileSummaryForSeeding(nil, &TestSeedError{TestName: "x", Err: errors.New("e")})
	reconcileSummaryForSeeding(&workspace.TestSummary{}, nil)
}

func TestReconcileSummaryForSeeding_FlipsPassesAndRebuildsFailures(t *testing.T) {
	t.Parallel()
	first := &TestSeedError{TestName: "bad", Err: errors.New("broken sql")}
	summary := &workspace.TestSummary{
		TotalRuns: 3,
		Results: []workspace.TestResult{
			{TestName: "bad", Status: "fail", Reason: first.Error(), DurationMs: 5},
			{TestName: "good_a", Status: "pass", DurationMs: 100},
			{TestName: "good_b", Status: "pass", DurationMs: 90},
		},
		Failures: []workspace.TestFailure{
			{TestName: "bad", Reason: first.Error(), DurationMs: 5},
		},
		Passed: 2,
		Failed: 1,
	}

	reconcileSummaryForSeeding(summary, first)

	if summary.Passed != 0 {
		t.Fatalf("Passed: want 0, got %d", summary.Passed)
	}
	if summary.Failed != 3 {
		t.Fatalf("Failed: want 3, got %d", summary.Failed)
	}
	if len(summary.Failures) != 3 {
		t.Fatalf("Failures: want 3 entries, got %d: %+v", len(summary.Failures), summary.Failures)
	}
	cascade := "suite aborted because seeding failed in test \"bad\": broken sql"
	for _, f := range summary.Failures {
		switch f.TestName {
		case "bad":
			if f.Reason != first.Error() {
				t.Errorf("bad: want original seed reason, got %q", f.Reason)
			}
		case "good_a", "good_b":
			if f.Reason != cascade {
				t.Errorf("%s: want cascade reason, got %q", f.TestName, f.Reason)
			}
		default:
			t.Errorf("unexpected failure row %q", f.TestName)
		}
	}
}

func TestReconcileSummaryForSeeding_NoPassesOnlySeedFailure(t *testing.T) {
	t.Parallel()
	first := &TestSeedError{TestName: "only", Err: errors.New("x")}
	summary := &workspace.TestSummary{
		TotalRuns: 1,
		Results: []workspace.TestResult{
			{TestName: "only", Status: "fail", Reason: first.Error()},
		},
		Failures: []workspace.TestFailure{{TestName: "only", Reason: first.Error()}},
		Passed:    0,
		Failed:    1,
	}
	reconcileSummaryForSeeding(summary, first)
	if summary.Failed != 1 || len(summary.Failures) != 1 {
		t.Fatalf("got Failed=%d failures=%d", summary.Failed, len(summary.Failures))
	}
	if summary.Failures[0].Reason != first.Error() {
		t.Fatalf("reason: %q", summary.Failures[0].Reason)
	}
}

func TestRecordFirstSeedFailure_FirstWins(t *testing.T) {
	t.Parallel()
	e := NewEngine(&workspace.Workspace{})
	a := &TestSeedError{TestName: "a", Err: errors.New("first")}
	b := &TestSeedError{TestName: "b", Err: errors.New("second")}
	e.recordFirstSeedFailure(a)
	e.recordFirstSeedFailure(b)
	e.seedFailMu.Lock()
	got := e.firstSeedFail
	e.seedFailMu.Unlock()
	if got == nil || got.TestName != "a" {
		t.Fatalf("want first failure a, got %+v", got)
	}
}

func TestRecordFirstSeedFailure_NilArgsNoPanic(t *testing.T) {
	t.Parallel()
	var e *Engine
	e.recordFirstSeedFailure(nil)
	eng := NewEngine(&workspace.Workspace{})
	eng.recordFirstSeedFailure(nil)
}

func TestReconcileSummaryForSeeding_PreservesExpectedActualOnCascade(t *testing.T) {
	t.Parallel()
	first := &TestSeedError{TestName: "seed", Err: errors.New("x")}
	summary := &workspace.TestSummary{
		TotalRuns: 2,
		Results: []workspace.TestResult{
			{TestName: "seed", Status: "fail", Reason: first.Error()},
			{TestName: "other", Status: "pass", DurationMs: 1, Expected: "exp", Actual: "act"},
		},
		Passed: 1,
		Failed: 1,
	}
	reconcileSummaryForSeeding(summary, first)
	var other *workspace.TestFailure
	for i := range summary.Failures {
		if summary.Failures[i].TestName == "other" {
			other = &summary.Failures[i]
			break
		}
	}
	if other == nil || other.Expected != "exp" || other.Actual != "act" {
		t.Fatalf("want Expected/Actual preserved on cascade row, got %+v", other)
	}
}
