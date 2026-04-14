package workspace

import (
	"fmt"
	"strings"
	"time"
)

// IsWaitPerformTarget reports whether the perform line targets the built-in wait
// phase (case-insensitive).
func IsWaitPerformTarget(line ParsedLine) bool {
	return strings.EqualFold(strings.TrimSpace(line.Target), "wait")
}

// ParseWaitPerformDuration reads a Go duration from a Perform -> wait line.
// It accepts an explicit Duration clause (Duration: 500ms) or a single
// positional duration token (Perform -> wait -> 250ms), matching postgres
// positional style.
func ParseWaitPerformDuration(line ParsedLine) (time.Duration, error) {
	var durStr string
	var fromDurationClause bool

	for _, c := range line.Clauses {
		if c.Value != nil && strings.EqualFold(strings.TrimSpace(c.Key), "duration") {
			durStr = strings.TrimSpace(*c.Value)
			fromDurationClause = true
			break
		}
	}
	if !fromDurationClause {
		for _, c := range line.Clauses {
			if c.Value == nil {
				durStr = strings.TrimSpace(c.Key)
				break
			}
		}
	}

	if durStr == "" {
		return 0, fmt.Errorf("Perform -> wait requires a duration (Duration: <value> or positional e.g. 500ms)")
	}

	d, err := time.ParseDuration(durStr)
	if err != nil {
		return 0, fmt.Errorf("invalid wait duration %q: %w", durStr, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("wait duration must be positive, got %s", d)
	}
	return d, nil
}

// ValidateWaitPerformLine checks that a phased Perform -> wait line has a valid
// positive duration.
func ValidateWaitPerformLine(line ParsedLine) error {
	_, err := ParseWaitPerformDuration(line)
	return err
}
