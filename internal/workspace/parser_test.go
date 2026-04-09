package workspace_test

import (
	"testing"

	"dojo/internal/workspace"
)

func TestParsePlan(t *testing.T) {
	plan := `Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> postgres -> Request: postgres_request.sql
Expect -> gemini -> Request: gemini_request.json -> Evaluate Response
Expect -> whatsapp -> Request: whatsapp_request.json -> Respond: mock_response.json
`
	doc, err := workspace.ParsePlan(plan)
	if err != nil {
		t.Fatalf("failed to parse plan: %v", err)
	}

	if len(doc.Lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(doc.Lines))
	}

	if doc.Lines[0].Action != "Perform" || doc.Lines[0].Target != "entrypoints/webhook" {
		t.Errorf("line 1 parsed incorrectly: action=%q target=%q", doc.Lines[0].Action, doc.Lines[0].Target)
	}

	if len(doc.Lines[1].Clauses) != 1 {
		t.Fatalf("expected 1 clause for line 2, got %d", len(doc.Lines[1].Clauses))
	}
	if doc.Lines[1].Clauses[0].Key != "Request" || *doc.Lines[1].Clauses[0].Value != "postgres_request.sql" {
		t.Errorf("line 2 clause 1: key=%q value=%v", doc.Lines[1].Clauses[0].Key, doc.Lines[1].Clauses[0].Value)
	}

	if len(doc.Lines[2].Clauses) != 2 {
		t.Fatalf("expected 2 clauses for line 3 (Request + Evaluate Response), got %d", len(doc.Lines[2].Clauses))
	}
	if doc.Lines[2].Clauses[1].Key != "Evaluate Response" {
		t.Errorf("line 3 clause 2: expected 'Evaluate Response', got %q", doc.Lines[2].Clauses[1].Key)
	}

	if len(doc.Lines[3].Clauses) != 2 {
		t.Fatalf("expected 2 clauses for line 4 (Request + Respond), got %d", len(doc.Lines[3].Clauses))
	}
	if doc.Lines[3].Clauses[1].Key != "Respond" || *doc.Lines[3].Clauses[1].Value != "mock_response.json" {
		t.Errorf("line 4 clause 2: key=%q value=%v", doc.Lines[3].Clauses[1].Key, doc.Lines[3].Clauses[1].Value)
	}
}

func TestParsePlan_QuotedValues(t *testing.T) {
	plan := `Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> gemini -> Request: "gemini_request.json" -> Respond: gemini_response.json
`
	doc, err := workspace.ParsePlan(plan)
	if err != nil {
		t.Fatalf("failed to parse plan: %v", err)
	}

	if len(doc.Lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(doc.Lines))
	}

	geminiLine := doc.Lines[1]
	if geminiLine.Target != "gemini" {
		t.Fatalf("line 2 target: got %q", geminiLine.Target)
	}
	if len(geminiLine.Clauses) != 2 {
		t.Fatalf("line 2 clauses: expected 2, got %d", len(geminiLine.Clauses))
	}
	if *geminiLine.Clauses[0].Value != "gemini_request.json" {
		t.Errorf("quoted value should have quotes stripped: got %q", *geminiLine.Clauses[0].Value)
	}
}
