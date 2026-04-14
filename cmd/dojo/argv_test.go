package main

import "testing"

func TestArgvHasLLMUsage(t *testing.T) {
	if !argvHasLLMUsage([]string{"run", "./suite", "--llm-usage"}) {
		t.Fatal("expected true for trailing --llm-usage")
	}
	if !argvHasLLMUsage([]string{"run", "--llm-usage", "./suite"}) {
		t.Fatal("expected true for --llm-usage between run and path")
	}
	if argvHasLLMUsage([]string{"run", "./suite"}) {
		t.Fatal("expected false without flag")
	}
	if !argvHasLLMUsage([]string{"--llm-usage=1"}) {
		t.Fatal("expected true for --llm-usage=1")
	}
	if argvHasLLMUsage([]string{"--llm-usage=false"}) {
		t.Fatal("expected false for --llm-usage=false")
	}
}

func TestNextSuitePathFromArgs(t *testing.T) {
	got, err := nextSuitePathFromArgs([]string{"run", "--llm-usage", "./example/tests/blackbox"})
	if err != nil || got != "./example/tests/blackbox" {
		t.Fatalf("got %q err %v", got, err)
	}
	got, err = nextSuitePathFromArgs([]string{"run", "./suite", "--llm-usage"})
	if err != nil || got != "./suite" {
		t.Fatalf("trailing flag: got %q err %v", got, err)
	}
	got, err = nextSuitePathFromArgs([]string{"./suite", "--llm-usage"})
	if err != nil || got != "./suite" {
		t.Fatalf("no run: got %q err %v", got, err)
	}
	got, err = nextSuitePathFromArgs([]string{"run", "-o", "out", "./suite"})
	if err != nil || got != "./suite" {
		t.Fatalf("value flag: got %q err %v", got, err)
	}
	_, err = nextSuitePathFromArgs([]string{"run"})
	if err == nil {
		t.Fatal("expected error for run alone")
	}
}
