package engine

import (
	"context"
	"testing"
	"time"

	"github.com/elmacnifico/dojo/internal/workspace"
)

func TestExecuteWaitPerform(t *testing.T) {
	e := &Engine{}
	ctx := context.Background()
	line := workspace.ParsedLine{
		Action:  "Perform",
		Target:  "wait",
		Clauses: []workspace.ParsedClause{{Key: "1ms", Value: nil}},
	}
	start := time.Now()
	if err := e.executeWaitPerform(ctx, line); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < time.Millisecond {
		t.Fatalf("expected at least ~1ms elapsed, got %v", d)
	}
}

func TestExecuteWaitPerform_ContextCancel(t *testing.T) {
	e := &Engine{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	line := workspace.ParsedLine{
		Action:  "Perform",
		Target:  "wait",
		Clauses: []workspace.ParsedClause{{Key: "1h", Value: nil}},
	}
	err := e.executeWaitPerform(ctx, line)
	if err == nil {
		t.Fatal("expected context error")
	}
	if err != context.Canceled {
		t.Fatalf("got %v", err)
	}
}
