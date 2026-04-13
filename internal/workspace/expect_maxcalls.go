package workspace

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseMaxCallsFromExpectLine parses an optional MaxCalls clause from an Expect
// line. When present, the value must be a positive integer (matching
// [WireFixturesFromPlan]).
func ParseMaxCallsFromExpectLine(l ParsedLine) (max int, found bool, err error) {
	for _, clause := range l.Clauses {
		if clause.Value == nil {
			continue
		}
		if strings.ToLower(clause.Key) != "maxcalls" {
			continue
		}
		v := strings.TrimSpace(*clause.Value)
		m, convErr := strconv.Atoi(v)
		if convErr != nil {
			return 0, true, fmt.Errorf("MaxCalls must be an integer, got %q", *clause.Value)
		}
		if m < 1 {
			return 0, true, fmt.Errorf("MaxCalls must be at least 1, got %d", m)
		}
		return m, true, nil
	}
	return 0, false, nil
}
