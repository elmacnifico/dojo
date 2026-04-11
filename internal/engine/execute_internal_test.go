package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dojo/internal/workspace"
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



func TestReadFixture(t *testing.T) {
	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "test_foo")
	suiteDir := filepath.Join(tmpDir, "suite_bar")

	os.MkdirAll(testDir, 0755)
	os.MkdirAll(suiteDir, 0755)

	// Test fallback to suite dir
	suiteFile := filepath.Join(suiteDir, "query.sql")
	os.WriteFile(suiteFile, []byte("SELECT * FROM users"), 0644)
	
	b, err := readFixture(testDir, suiteDir, "query.sql")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if string(b) != "SELECT * FROM users" {
		t.Errorf("expected 'SELECT * FROM users', got %q", string(b))
	}

	// Test primary dir wins
	testFile := filepath.Join(testDir, "query.sql")
	os.WriteFile(testFile, []byte("SELECT 1"), 0644)
	
	b, err = readFixture(testDir, suiteDir, "query.sql")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if string(b) != "SELECT 1" {
		t.Errorf("expected 'SELECT 1', got %q", string(b))
	}

	// Test missing file
	_, err = readFixture(testDir, suiteDir, "missing.sql")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestExecutePostgresPerform_MissingQuery(t *testing.T) {
	e := NewEngine(&workspace.Workspace{})
	line := workspace.ParsedLine{
		Action: "Perform",
		Target: "postgres",
		Clauses: []workspace.ParsedClause{},
	}
	err := e.executePostgresPerform(context.Background(), nil, line, "/tmp", "/tmp", "")
	if err == nil {
		t.Fatal("expected error for missing query clause, got nil")
	}
}
