package engine

import (
	"errors"
	"testing"
)

func TestTestSeedError_Unwrap(t *testing.T) {
	t.Parallel()
	inner := errors.New("inner")
	e := &TestSeedError{TestName: "test_x", Err: inner}
	if !errors.Is(e, inner) {
		t.Fatalf("errors.Is(e, inner) want true")
	}
	if got := e.Error(); got == "" {
		t.Fatal("empty Error()")
	}
}
