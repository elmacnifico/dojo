package reporter_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"github.com/elmacnifico/dojo/internal/workspace"
	"github.com/elmacnifico/dojo/internal/reporter"
)

func TestReporter(t *testing.T) {
	tmpDir := t.TempDir()
	resultsDir := filepath.Join(tmpDir, "results")

	r := reporter.NewReporter(resultsDir)

	summary := workspace.TestSummary{
		TotalRuns: 2,
		Passed:    1,
		Failed:    1,
		Failures: []workspace.TestFailure{
			{
				TestName: "test_1",
				Expected: `{"status": "ok"}`,
				Actual:   `{"status": "error"}`,
				Diff:     `  {"status": "ok"} -> {"status": "error"}`,
				Reason:   "Expected status ok but got error",
			},
		},
	}

	err := r.Generate(summary)
	if err != nil {
		t.Fatalf("Failed to generate reports: %v", err)
	}

	jsonPath := filepath.Join(resultsDir, "summary.json")
	jsonBytes, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("Failed to read JSON summary: %v", err)
	}

	var parsedSummary workspace.TestSummary
	if err := json.Unmarshal(jsonBytes, &parsedSummary); err != nil {
		t.Fatalf("Failed to parse JSON summary: %v", err)
	}
	if parsedSummary.TotalRuns != 2 || parsedSummary.Failed != 1 {
		t.Errorf("JSON summary values incorrect: %+v", parsedSummary)
	}

	mdPath := filepath.Join(resultsDir, "summary.md")
	mdBytes, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("Failed to read MD summary: %v", err)
	}

	mdContent := string(mdBytes)
	if !strings.Contains(mdContent, "**Total Runs:** 2") {
		t.Errorf("MD summary missing total runs")
	}
}

func TestReporter_AllPassed(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	r := reporter.NewReporter(filepath.Join(tmpDir, "results"))
	summary := workspace.TestSummary{TotalRuns: 5, Passed: 5, Failed: 0}
	if err := r.Generate(summary); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	md, err := os.ReadFile(filepath.Join(tmpDir, "results", "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(md), "## Failures") {
		t.Error("should not contain Failures section when all tests pass")
	}
}

func TestReporter_JSONRoundtrip(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	r := reporter.NewReporter(filepath.Join(tmpDir, "results"))
	summary := workspace.TestSummary{
		TotalRuns: 3,
		Passed:    2,
		Failed:    1,
		Failures: []workspace.TestFailure{
			{TestName: "test_fail", Reason: "assertion error"},
		},
	}
	if err := r.Generate(summary); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(tmpDir, "results", "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got workspace.TestSummary
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.TotalRuns != 3 || got.Passed != 2 || got.Failed != 1 {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	if len(got.Failures) != 1 || got.Failures[0].TestName != "test_fail" {
		t.Errorf("failures mismatch: %+v", got.Failures)
	}
}
