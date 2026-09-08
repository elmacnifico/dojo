package engine

import (
	"strings"
)

// maxDiffLines caps the rendered line diff so huge payloads (multi-KiB JSON
// bodies, Postgres wire dumps) cannot blow up test summaries.
const maxDiffLines = 40

// lineDiff renders a minimal unified-diff-style comparison of two multi-line
// payloads. Each line of expected is prefixed with "-", each line of actual
// with "+"; lines common to both (in order) are prefixed with " ". When the
// inputs are single-line or identical, the common lines are the payload
// itself, which still reads well in reports.
//
// The output is capped at [maxDiffLines] lines with a truncation marker so
// reports stay readable for large payloads.
func lineDiff(expected, actual string) string {
	expLines := splitLines(expected)
	actLines := splitLines(actual)

	// Longest common subsequence (dynamic programming) to align the lines.
	lcs := make([][]int, len(expLines)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(actLines)+1)
	}
	for i := len(expLines) - 1; i >= 0; i-- {
		for j := len(actLines) - 1; j >= 0; j-- {
			if expLines[i] == actLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}

	var out strings.Builder
	emitted := 0
	truncated := false
	emit := func(prefix, line string) {
		if emitted >= maxDiffLines {
			truncated = true
			return
		}
		out.WriteString(prefix)
		out.WriteString(line)
		out.WriteByte('\n')
		emitted++
	}

	i, j := 0, 0
	for i < len(expLines) && j < len(actLines) {
		switch {
		case expLines[i] == actLines[j]:
			emit(" ", expLines[i])
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			emit("-", expLines[i])
			i++
		default:
			emit("+", actLines[j])
			j++
		}
	}
	for ; i < len(expLines); i++ {
		emit("-", expLines[i])
	}
	for ; j < len(actLines); j++ {
		emit("+", actLines[j])
	}

	if truncated {
		out.WriteString("  ... (diff truncated)\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

// splitLines splits s into lines, tolerating CRLF and omitting a trailing
// empty element.
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// newMismatchError builds a [MismatchError] for payload mismatches, rendering
// a line diff of the expected and actual payloads for fast human triage.
func newMismatchError(reason, expected, actual string) *MismatchError {
	return &MismatchError{
		Reason:   reason,
		Expected: expected,
		Actual:   actual,
		Diff:     lineDiff(expected, actual),
	}
}
