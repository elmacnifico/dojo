// Package reporter handles outputting execution results to the filesystem.
package reporter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"dojo/internal/workspace"
)

// Reporter handles outputting execution results to the filesystem.
type Reporter struct {
	OutputDir string
}

// NewReporter creates a Reporter pointing to a target output directory.
func NewReporter(outputDir string) *Reporter {
	return &Reporter{OutputDir: outputDir}
}

// Generate creates both JSON and Markdown summaries of the test execution.
func (r *Reporter) Generate(summary workspace.TestSummary) error {
	if err := os.MkdirAll(r.OutputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// 1. Write summary.json
	jsonPath := filepath.Join(r.OutputDir, "summary.json")
	jsonBytes, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal json summary: %w", err)
	}
	if err := os.WriteFile(jsonPath, jsonBytes, 0644); err != nil {
		return fmt.Errorf("failed to write json summary: %w", err)
	}

	// 2. Write summary.md
	mdPath := filepath.Join(r.OutputDir, "summary.md")
	mdContent, err := renderMarkdownSummary(summary)
	if err != nil {
		return fmt.Errorf("failed to render markdown summary: %w", err)
	}
	if err := os.WriteFile(mdPath, []byte(mdContent), 0644); err != nil {
		return fmt.Errorf("failed to write markdown summary: %w", err)
	}

	return nil
}

func renderMarkdownSummary(summary workspace.TestSummary) (string, error) {
	tmpl := `# Dojo Test Summary

**Total Runs:** {{.TotalRuns}}
**Passed:** {{.Passed}}
**Failed:** {{.Failed}}
{{- if .DurationMs}}
**Duration:** {{.DurationMs}}ms
{{- end}}

{{- if .Results}}

## Results

| Test | Status | Duration |
|------|--------|----------|
{{- range .Results}}
| {{.TestName}} | {{.Status}} | {{.DurationMs}}ms |
{{- end}}
{{end}}

{{if .Failures}}## Failures

{{range .Failures}}### {{.TestName}}

**Reason:** {{.Reason}}
{{if .Expected}}
**Expected:**
` + "```json\n{{.Expected}}\n```" + `
{{end}}{{if .Actual}}
**Actual:**
` + "```json\n{{.Actual}}\n```" + `
{{end}}{{if .Diff}}
**Diff:**
` + "```diff\n{{.Diff}}\n```" + `
{{end}}
{{end}}{{else}}All tests passed.{{end}}`

	t, err := template.New("summary").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parsing summary template: %w", err)
	}

	var buf strings.Builder
	if err := t.Execute(&buf, summary); err != nil {
		return "", fmt.Errorf("executing summary template: %w", err)
	}

	return buf.String(), nil
}
