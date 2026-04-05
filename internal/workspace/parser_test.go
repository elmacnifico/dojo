package workspace_test

import (
	"testing"
	"dojo/internal/workspace"
)

func TestParsePlan(t *testing.T) {
	plan := `Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> postgres -> Query: "INSERT INTO users" -> Payload: user_seed.json
Expect -> gemini -> Payload: request.json -> Evaluate Response
Expect -> whatsapp -> Payload: "Success" -> Respond: mock_response.json
`
	doc, err := workspace.ParsePlan(plan)
	if err != nil {
		t.Fatalf("failed to parse plan: %v", err)
	}

	if len(doc.Lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(doc.Lines))
	}

	if doc.Lines[0].Action != "Perform" || doc.Lines[0].Target != "entrypoints/webhook" {
		t.Errorf("line 1 parsed incorrectly")
	}

	if len(doc.Lines[1].Clauses) != 2 {
		t.Fatalf("expected 2 clauses for line 2")
	}
	if doc.Lines[1].Clauses[0].Key != "Query" || *doc.Lines[1].Clauses[0].Value != "INSERT INTO users" {
		t.Errorf("line 2 clause 1 parsed incorrectly")
	}
}

func TestParsePlan_CorrelateClause(t *testing.T) {
	plan := `Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> postgres -> Correlate: "+1234567890" -> Query: "SELECT user_id FROM users"
Expect -> gemini -> Correlate: "usr_42" -> Request: gemini_request.json -> Respond: gemini_response.json
`
	doc, err := workspace.ParsePlan(plan)
	if err != nil {
		t.Fatalf("failed to parse plan: %v", err)
	}

	if len(doc.Lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(doc.Lines))
	}

	pgLine := doc.Lines[1]
	if pgLine.Target != "postgres" {
		t.Fatalf("line 2 target: got %q", pgLine.Target)
	}
	if len(pgLine.Clauses) != 2 {
		t.Fatalf("line 2 clauses: expected 2, got %d", len(pgLine.Clauses))
	}
	if pgLine.Clauses[0].Key != "Correlate" || *pgLine.Clauses[0].Value != "+1234567890" {
		t.Errorf("line 2 clause 0: got key=%q value=%v", pgLine.Clauses[0].Key, pgLine.Clauses[0].Value)
	}

	geminiLine := doc.Lines[2]
	if geminiLine.Clauses[0].Key != "Correlate" || *geminiLine.Clauses[0].Value != "usr_42" {
		t.Errorf("line 3 clause 0: got key=%q value=%v", geminiLine.Clauses[0].Key, geminiLine.Clauses[0].Value)
	}
}
