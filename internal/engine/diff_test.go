package engine

import (
	"strings"
	"testing"
)

func TestLineDiff(t *testing.T) {
	t.Parallel()

	t.Run("identical payloads", func(t *testing.T) {
		t.Parallel()
		got := lineDiff("line1\nline2", "line1\nline2")
		want := " line1\n line2"
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	})

	t.Run("changed middle line", func(t *testing.T) {
		t.Parallel()
		got := lineDiff("a\nfoo\nb", "a\nbar\nb")
		want := " a\n-foo\n+bar\n b"
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	})

	t.Run("inserted and removed lines", func(t *testing.T) {
		t.Parallel()
		got := lineDiff("a\nb", "a\nx\nb\nc")
		want := " a\n+x\n b\n+c"
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	})

	t.Run("empty expected", func(t *testing.T) {
		t.Parallel()
		got := lineDiff("", "one")
		if got != "+one" {
			t.Errorf("got %q want %q", got, "+one")
		}
	})

	t.Run("single-line payloads render as minus plus", func(t *testing.T) {
		t.Parallel()
		got := lineDiff(`{"status": "ok"}`, `{"status": "error"}`)
		want := `-{"status": "ok"}\n+{"status": "error"}`
		want = strings.ReplaceAll(want, `\n`, "\n")
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	})

	t.Run("truncates at maxDiffLines", func(t *testing.T) {
		t.Parallel()
		var exp, act strings.Builder
		for i := 0; i < 60; i++ {
			exp.WriteString("same\n")
			act.WriteString("same\n")
		}
		exp.WriteString("only-in-expected\n")
		act.WriteString("only-in-actual\n")
		got := lineDiff(exp.String(), act.String())
		if lines := len(splitLines(got)); lines != maxDiffLines+1 {
			t.Errorf("expected %d lines (cap + truncation marker), got %d", maxDiffLines+1, lines)
		}
		if !strings.Contains(got, "... (diff truncated)") {
			t.Errorf("expected truncation marker, got:\n%s", got)
		}
	})
}
