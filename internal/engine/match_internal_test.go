package engine

import (
	"testing"

	"dojo/internal/workspace"

	"github.com/jackc/pgproto3/v2"
)

func TestPgResponseCheck_ReadyForQuery(t *testing.T) {
	t.Parallel()
	rfq, _ := (&pgproto3.ReadyForQuery{TxStatus: 'I'}).Encode(nil)

	complete, errMsg := pgResponseCheck(rfq)
	if !complete {
		t.Fatal("expected complete=true for ReadyForQuery")
	}
	if errMsg != "" {
		t.Fatalf("expected empty errMsg, got %q", errMsg)
	}
}

func TestPgResponseCheck_ErrorResponse(t *testing.T) {
	t.Parallel()
	er := &pgproto3.ErrorResponse{
		Severity: "ERROR",
		Message:  "relation \"bogus\" does not exist",
	}
	data, _ := er.Encode(nil)

	complete, errMsg := pgResponseCheck(data)
	if !complete {
		t.Fatal("expected complete=true for ErrorResponse")
	}
	if errMsg == "" {
		t.Fatal("expected non-empty errMsg for ErrorResponse")
	}
	if errMsg != "relation \"bogus\" does not exist" {
		t.Fatalf("unexpected errMsg: %q", errMsg)
	}
}

func TestPgResponseCheck_Incomplete(t *testing.T) {
	t.Parallel()
	complete, errMsg := pgResponseCheck([]byte{})
	if complete {
		t.Fatal("expected complete=false for empty input")
	}
	if errMsg != "" {
		t.Fatalf("expected empty errMsg, got %q", errMsg)
	}
}

func TestPgResponseCheck_CommandCompleteThenReadyForQuery(t *testing.T) {
	t.Parallel()
	cc, _ := (&pgproto3.CommandComplete{CommandTag: []byte("INSERT 0 1")}).Encode(nil)
	rfq, _ := (&pgproto3.ReadyForQuery{TxStatus: 'I'}).Encode(nil)
	data := append(cc, rfq...)

	complete, errMsg := pgResponseCheck(data)
	if !complete {
		t.Fatal("expected complete=true")
	}
	if errMsg != "" {
		t.Fatalf("expected empty errMsg, got %q", errMsg)
	}
}

func TestProcessRequest_TestLevelOverrideWithoutExpect(t *testing.T) {
	t.Parallel()
	eng := &Engine{
		Registry: NewRegistry(),
	}
	eng.ActiveSuite = &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"download": {
				Mode: "mock",
				DefaultResponse: &workspace.DefaultResponse{
					Code:    200,
					Payload: []byte(`suite-level`),
				},
			},
		},
	}
	at := &ActiveTest{
		ID: "t1",
		Test: &workspace.Test{
			APIs: map[string]workspace.APIConfig{
				"download": {
					Mode: "mock",
					DefaultResponse: &workspace.DefaultResponse{
						Code:        200,
						ContentType: "image/jpeg",
						Payload:     []byte(`test-level-binary`),
					},
				},
			},
		},
		Expectations: map[string][]*Expectation{},
		done:         make(chan struct{}),
	}
	eng.Registry.Register("t1", at)

	result := eng.ProcessRequest("http", "download", []byte(`anything`))
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if !result.IsMock {
		t.Fatal("expected IsMock=true")
	}
	if result.MockContentType != "image/jpeg" {
		t.Fatalf("expected content type image/jpeg, got %q", result.MockContentType)
	}
	if string(result.MockResponse) != "test-level-binary" {
		t.Fatalf("expected test-level-binary response, got %q", string(result.MockResponse))
	}
}

func TestSplitPhases_SinglePhase(t *testing.T) {
	t.Parallel()
	lines := []workspace.ParsedLine{
		{Action: "Perform", Target: "entrypoints/webhook"},
		{Action: "Expect", Target: "gemini"},
		{Action: "Expect", Target: "postgres"},
	}
	phases := splitPhases(lines)
	if len(phases) != 1 {
		t.Fatalf("expected 1 phase, got %d", len(phases))
	}
	if len(phases[0].expects) != 2 {
		t.Fatalf("expected 2 expects in phase 0, got %d", len(phases[0].expects))
	}
}

func TestSplitPhases_TwoPhases(t *testing.T) {
	t.Parallel()
	lines := []workspace.ParsedLine{
		{Action: "Perform", Target: "entrypoints/webhook"},
		{Action: "Expect", Target: "gemini"},
		{Action: "Expect", Target: "postgres"},
		{Action: "Perform", Target: "postgres"},
	}
	phases := splitPhases(lines)
	if len(phases) != 2 {
		t.Fatalf("expected 2 phases, got %d", len(phases))
	}
	if len(phases[0].expects) != 2 {
		t.Fatalf("expected 2 expects in phase 0, got %d", len(phases[0].expects))
	}
	if len(phases[1].expects) != 0 {
		t.Fatalf("expected 0 expects in phase 1, got %d", len(phases[1].expects))
	}
	if phases[1].perform.Target != "postgres" {
		t.Fatalf("expected phase 1 target = postgres, got %q", phases[1].perform.Target)
	}
}
