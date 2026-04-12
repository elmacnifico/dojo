package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePostgresPerformLine_MissingQuery(t *testing.T) {
	t.Parallel()
	line := ParsedLine{Action: "Perform", Target: "postgres", Clauses: []ParsedClause{}}
	if err := ValidatePostgresPerformLine(line, t.TempDir(), t.TempDir()); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidatePostgresPerformLine_QueryClauseForm(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "q.sql"), []byte("SELECT 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	line := ParsedLine{
		Action: "Perform",
		Target: "postgres",
		Clauses: []ParsedClause{
			{Key: "Query", Value: ptr("q.sql")},
		},
	}
	if err := ValidatePostgresPerformLine(line, tmp, tmp); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePostgresPerformLine_ExpectJSONMissing(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "q.sql"), []byte("SELECT 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	line := ParsedLine{
		Action: "Perform",
		Target: "postgres",
		Clauses: []ParsedClause{
			{Key: "Query", Value: ptr("q.sql")},
			{Key: "Expect", Value: ptr("gone.json")},
		},
	}
	if err := ValidatePostgresPerformLine(line, tmp, tmp); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidatePostgresPerformLine_ExpectInvalidNonNumeric(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "q.sql"), []byte("SELECT 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	line := ParsedLine{
		Action: "Perform",
		Target: "postgres",
		Clauses: []ParsedClause{
			{Key: "Query", Value: ptr("q.sql")},
			{Key: "Expect", Value: ptr("not-a-number")},
		},
	}
	if err := ValidatePostgresPerformLine(line, tmp, tmp); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidatePostgresPerformLine_ExpectJSONFileOK(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "q.sql"), []byte("SELECT 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "exp.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	line := ParsedLine{
		Action: "Perform",
		Target: "postgres",
		Clauses: []ParsedClause{
			{Key: "Query", Value: ptr("q.sql")},
			{Key: "Expect", Value: ptr("exp.json")},
		},
	}
	if err := ValidatePostgresPerformLine(line, tmp, tmp); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePostgresPerformLine_QueryReadErrorWrapped(t *testing.T) {
	t.Parallel()
	line := ParsedLine{
		Action: "Perform",
		Target: "postgres",
		Clauses: []ParsedClause{
			{Key: "Query", Value: ptr("missing.sql")},
		},
	}
	err := ValidatePostgresPerformLine(line, t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
}

func ptr(s string) *string { return &s }
