package engine

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elmacnifico/dojo/internal/workspace"
)

func TestPrepareStartupPlan(t *testing.T) {
	ws := &workspace.Workspace{
		BaseDir: "/tmp",
		Suites:  make(map[string]*workspace.Suite),
	}
	e := NewEngine(ws)

	suite := &workspace.Suite{
		Config: workspace.DojoConfig{
			Timeouts: workspace.TimeoutConfig{
				Expect: workspace.Duration{Duration: time.Second},
			},
		},
		APIs: map[string]workspace.APIConfig{
			"gemini": {Mode: "mock"},
		},
		StartupPlan: "Expect -> gemini\n",
	}

	ctx := context.Background()
	at, err := e.prepareStartupPlan(ctx, suite, "test_suite")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if at.ID != "startup" {
		t.Errorf("expected active test ID 'startup', got %q", at.ID)
	}
	if len(at.Expectations["gemini"]) != 1 {
		t.Errorf("expected 1 gemini expectation, got %d", len(at.Expectations["gemini"]))
	}
}

func TestPrepareStartupPlan_InvalidAction(t *testing.T) {
	ws := &workspace.Workspace{
		BaseDir: "/tmp",
		Suites:  make(map[string]*workspace.Suite),
	}
	e := NewEngine(ws)

	suite := &workspace.Suite{
		StartupPlan: "Perform -> POST /test\n",
	}

	ctx := context.Background()
	_, err := e.prepareStartupPlan(ctx, suite, "test_suite")
	if err == nil {
		t.Fatal("expected error for non-Expect action, got nil")
	}
}

func TestAwaitPhaseExpectations_Success(t *testing.T) {
	e := NewEngine(&workspace.Workspace{})
	at := &ActiveTest{
		ID: "test",
		Expectations: map[string][]*Expectation{
			"api1": {
				{Target: "api1", Index: 0, Deadline: 100 * time.Millisecond},
			},
		},
		done: make(chan struct{}),
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		at.MarkFulfilled("api1", 0, nil)
	}()

	err := e.awaitPhaseExpectations(context.Background(), at)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestAwaitPhaseExpectations_Timeout(t *testing.T) {
	e := NewEngine(&workspace.Workspace{})
	at := &ActiveTest{
		ID: "test",
		Expectations: map[string][]*Expectation{
			"api1": {
				{Target: "api1", Index: 0, Deadline: 10 * time.Millisecond},
			},
		},
		done: make(chan struct{}),
	}

	err := e.awaitPhaseExpectations(context.Background(), at)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestReadPlanFixture(t *testing.T) {
	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "test_foo")
	suiteDir := filepath.Join(tmpDir, "suite_bar")

	os.MkdirAll(testDir, 0755)
	os.MkdirAll(suiteDir, 0755)

	// Test fallback to suite dir
	suiteFile := filepath.Join(suiteDir, "query.sql")
	os.WriteFile(suiteFile, []byte("SELECT * FROM users"), 0644)


	b, err := workspace.ReadPlanFixture(testDir, suiteDir, "query.sql")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if string(b) != "SELECT * FROM users" {
		t.Errorf("expected 'SELECT * FROM users', got %q", string(b))
	}

	// Test primary dir wins
	testFile := filepath.Join(testDir, "query.sql")
	os.WriteFile(testFile, []byte("SELECT 1"), 0644)

	b, err = workspace.ReadPlanFixture(testDir, suiteDir, "query.sql")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if string(b) != "SELECT 1" {
		t.Errorf("expected 'SELECT 1', got %q", string(b))
	}

	// Test missing file
	_, err = workspace.ReadPlanFixture(testDir, suiteDir, "missing.sql")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestTriggerEntrypoint_StatusMismatchNoStdout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	suite := &workspace.Suite{
		Config: workspace.DojoConfig{
			Timeouts: workspace.TimeoutConfig{Perform: workspace.Duration{Duration: 2 * time.Second}},
		},
	}
	e := NewEngine(&workspace.Workspace{})
	ep := workspace.EntrypointConfig{Type: "http", Path: "/trigger", URL: srv.URL}

	// Capture stdout during the trigger so we can prove no debug output leaks.
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	triggerErr := e.triggerEntrypoint(context.Background(), suite, ep, nil, 200)
	w.Close()
	os.Stdout = orig

	out, _ := io.ReadAll(r)
	if len(bytes.TrimSpace(out)) != 0 {
		t.Errorf("expected no stdout output on status mismatch, got %q", string(out))
	}
	if triggerErr == nil {
		t.Fatal("expected a MismatchError from status mismatch, got nil")
	}
}

func TestExecutePostgresPerform_MissingQuery(t *testing.T) {
	e := NewEngine(&workspace.Workspace{})
	line := workspace.ParsedLine{
		Action:  "Perform",
		Target:  "postgres",
		Clauses: []workspace.ParsedClause{},
	}
	err := e.executePostgresPerform(context.Background(), nil, line, "/tmp", "/tmp", "")
	if err == nil {
		t.Fatal("expected error for missing query clause, got nil")
	}
}

// TestAwaitPhaseExpectations_DeadlineGradesStoredLiveResponse verifies the
// deadline backstop for live multi-round eval flows: when a flow records a
// response but ends with fewer rounds than the MaxCalls budget, the deadline
// goroutine grades the stored final response via evalDeadline instead of
// failing with "timed out waiting for expected request". The expectation
// deadline (150ms) fires long before the quiet window (default 8s), so the
// only path that can mark the expectation fulfilled here is FinalLiveResponse.
func TestAwaitPhaseExpectations_DeadlineGradesStoredLiveResponse(t *testing.T) {
	suite := &workspace.Suite{
		Config: workspace.DojoConfig{},
		APIs: map[string]workspace.APIConfig{
			"gemini": {Mode: "live", URL: "https://example.com"},
		},
	}
	active := &ActiveTest{
		ID:    "t1",
		Test:  &workspace.Test{APIs: map[string]workspace.APIConfig{}, Eval: "must be a valid response"},
		Suite: suite,
		Expectations: map[string][]*Expectation{
			"gemini": {{
				Target:       "gemini",
				Index:        0,
				RequiresEval: true,
				Deadline:     150 * time.Millisecond,
			}},
		},
		done: make(chan struct{}),
	}
	e := NewEngine(&workspace.Workspace{})
	e.ActiveSuite = suite
	e.Registry.Register("t1", active)

	// One matched round with no MaxCalls budget consumed: the response is
	// recorded and stays pending (no evaluator config -> grading fails, but
	// crucially it must fail with the EVALUATION error, not the timeout
	// error, proving evalDeadline ran on the stored payload).
	e.ProcessResponse("http", "t1", "gemini", nil, []byte(`{"candidates":[{"content":"response"}]}`))

	err := e.awaitPhaseExpectations(context.Background(), active)
	if err == nil {
		t.Fatal("expected error: no evaluator config is set, so grading the stored response must fail")
	}
	if !strings.Contains(err.Error(), "evaluator config missing in dojo.yaml") {
		t.Fatalf("expected the deadline to grade the stored live response (evaluator config error), got: %v", err)
	}
	if strings.Contains(err.Error(), "timed out") {
		t.Fatalf("deadline must grade the stored response, not time out; got: %v", err)
	}
}

// TestAwaitPhaseExpectations_DeadlineTimesOutWithoutLiveResponse is the
// mirror: no response ever recorded, so the deadline cannot grade anything
// and must produce the ordinary timeout error.
func TestAwaitPhaseExpectations_DeadlineTimesOutWithoutLiveResponse(t *testing.T) {
	suite := &workspace.Suite{
		Config: workspace.DojoConfig{},
		APIs: map[string]workspace.APIConfig{
			"gemini": {Mode: "live", URL: "https://example.com"},
		},
	}
	active := &ActiveTest{
		ID:    "t1",
		Test:  &workspace.Test{APIs: map[string]workspace.APIConfig{}},
		Suite: suite,
		Expectations: map[string][]*Expectation{
			"gemini": {{
				Target:       "gemini",
				Index:        0,
				RequiresEval: true,
				Deadline:     150 * time.Millisecond,
			}},
		},
		done: make(chan struct{}),
	}
	e := NewEngine(&workspace.Workspace{})
	e.ActiveSuite = suite
	e.Registry.Register("t1", active)

	err := e.awaitPhaseExpectations(context.Background(), active)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error for expectation without any recorded response, got: %v", err)
	}
}
