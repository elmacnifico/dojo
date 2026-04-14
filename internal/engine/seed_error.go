package engine

import (
	"fmt"

	"github.com/elmacnifico/dojo/internal/workspace"
)

// TestSeedError is returned when applying a test's seed/*.sql scripts against
// live Postgres fails. [RunSuite] uses it to detect seed failures and cascade
// suite-wide failure so CI does not report a mix of pass and fail when DB
// setup is broken.
type TestSeedError struct {
	TestName string
	Err      error
}

func (e *TestSeedError) Error() string {
	return fmt.Sprintf("test seeding failed (test %q): %v", e.TestName, e.Err)
}

func (e *TestSeedError) Unwrap() error {
	return e.Err
}

func (e *Engine) recordFirstSeedFailure(ts *TestSeedError) {
	if e == nil || ts == nil {
		return
	}
	e.seedFailMu.Lock()
	defer e.seedFailMu.Unlock()
	if e.firstSeedFail != nil {
		return
	}
	cpy := *ts
	e.firstSeedFail = &cpy
}

// reconcileSummaryForSeeding marks every passing test as failed when any test
// hit a seed error, so the suite summary reflects a full abort.
func reconcileSummaryForSeeding(summary *workspace.TestSummary, first *TestSeedError) {
	if summary == nil || first == nil {
		return
	}
	cascade := fmt.Sprintf("suite aborted because seeding failed in test %q: %v", first.TestName, first.Err)

	for i := range summary.Results {
		r := &summary.Results[i]
		if r.Status != "pass" {
			continue
		}
		r.Status = "fail"
		r.Reason = cascade
	}

	summary.Passed = 0
	summary.Failed = 0
	for _, r := range summary.Results {
		if r.Status == "pass" {
			summary.Passed++
		} else {
			summary.Failed++
		}
	}

	summary.Failures = nil
	for _, r := range summary.Results {
		if r.Status != "fail" {
			continue
		}
		f := workspace.TestFailure{
			TestName:   r.TestName,
			Reason:     r.Reason,
			DurationMs: r.DurationMs,
		}
		if r.Expected != "" {
			f.Expected = r.Expected
		}
		if r.Actual != "" {
			f.Actual = r.Actual
		}
		summary.Failures = append(summary.Failures, f)
	}
}
