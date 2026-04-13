package workspace

import (
	"strings"
	"testing"
)

func TestParseMaxCallsFromExpectLine(t *testing.T) {
	t.Parallel()

	ptr := func(s string) *string { return &s }

	t.Run("omitted", func(t *testing.T) {
		t.Parallel()
		line := ParsedLine{Clauses: []ParsedClause{
			{Key: "Request", Value: ptr("x.json")},
		}}
		max, found, err := ParseMaxCallsFromExpectLine(line)
		if err != nil || found || max != 0 {
			t.Fatalf("got max=%d found=%v err=%v", max, found, err)
		}
	})

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		line := ParsedLine{Clauses: []ParsedClause{
			{Key: "maxcalls", Value: ptr("5")},
		}}
		max, found, err := ParseMaxCallsFromExpectLine(line)
		if err != nil || !found || max != 5 {
			t.Fatalf("got max=%d found=%v err=%v", max, found, err)
		}
	})

	t.Run("invalid not integer", func(t *testing.T) {
		t.Parallel()
		line := ParsedLine{Clauses: []ParsedClause{
			{Key: "maxcalls", Value: ptr("nope")},
		}}
		_, _, err := ParseMaxCallsFromExpectLine(line)
		if err == nil || !strings.Contains(err.Error(), "integer") {
			t.Fatalf("expected integer parse error, got %v", err)
		}
	})

	t.Run("invalid zero", func(t *testing.T) {
		t.Parallel()
		line := ParsedLine{Clauses: []ParsedClause{
			{Key: "maxcalls", Value: ptr("0")},
		}}
		_, _, err := ParseMaxCallsFromExpectLine(line)
		if err == nil || !strings.Contains(err.Error(), "at least 1") {
			t.Fatalf("expected min value error, got %v", err)
		}
	})
}
