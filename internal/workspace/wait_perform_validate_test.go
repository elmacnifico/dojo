package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseWaitPerformDuration_DurationClause(t *testing.T) {
	line := ParsedLine{
		Action: "Perform",
		Target: "wait",
		Clauses: []ParsedClause{
			{Key: "Duration", Value: ptr("500ms")},
		},
	}
	d, err := ParseWaitPerformDuration(line)
	if err != nil {
		t.Fatal(err)
	}
	if d != 500*time.Millisecond {
		t.Fatalf("got %v", d)
	}
}

func TestParseWaitPerformDuration_Positional(t *testing.T) {
	line := ParsedLine{
		Action: "Perform",
		Target: "wait",
		Clauses: []ParsedClause{
			{Key: "1ms", Value: nil},
		},
	}
	d, err := ParseWaitPerformDuration(line)
	if err != nil {
		t.Fatal(err)
	}
	if d != time.Millisecond {
		t.Fatalf("got %v", d)
	}
}

func TestParseWaitPerformDuration_DurationClauseWinsOverPositional(t *testing.T) {
	line := ParsedLine{
		Action: "Perform",
		Target: "wait",
		Clauses: []ParsedClause{
			{Key: "1s", Value: nil},
			{Key: "Duration", Value: ptr("2s")},
		},
	}
	d, err := ParseWaitPerformDuration(line)
	if err != nil {
		t.Fatal(err)
	}
	if d != 2*time.Second {
		t.Fatalf("expected 2s from clause, got %v", d)
	}
}

func TestParseWaitPerformDuration_Missing(t *testing.T) {
	line := ParsedLine{Action: "Perform", Target: "wait"}
	_, err := ParseWaitPerformDuration(line)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseWaitPerformDuration_Invalid(t *testing.T) {
	line := ParsedLine{
		Action:  "Perform",
		Target:  "wait",
		Clauses: []ParsedClause{{Key: "Duration", Value: ptr("not-a-duration")}},
	}
	_, err := ParseWaitPerformDuration(line)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseWaitPerformDuration_NonPositive(t *testing.T) {
	line := ParsedLine{
		Action:  "Perform",
		Target:  "wait",
		Clauses: []ParsedClause{{Key: "Duration", Value: ptr("0s")}},
	}
	_, err := ParseWaitPerformDuration(line)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateTestPlanPerformPhases_WaitWithExpectFails(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "incoming.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	suite := &Suite{
		Entrypoints: map[string]EntrypointConfig{
			"webhook": {Type: "http", Method: "POST", Path: "/trigger"},
		},
	}
	test := &Test{
		Plan: `Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> gemini
Perform -> wait -> 1ms
Expect -> whatsapp
`,
	}
	err := ValidateTestPlanPerformPhases(suite, test, tmp, tmp)
	if err == nil {
		t.Fatal("expected error for Expect lines in a Perform -> wait phase")
	}
}
