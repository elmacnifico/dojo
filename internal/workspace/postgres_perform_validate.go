package workspace

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// ValidatePostgresPerformLine checks that a phased Perform -> postgres line can
// read its query (and optional JSON expect) fixtures before the suite runs.
func ValidatePostgresPerformLine(line ParsedLine, testDir, suiteDir string) error {
	var queryFile, expectValue string
	positionalCount := 0

	for _, c := range line.Clauses {
		if c.Value == nil {
			if positionalCount == 0 {
				queryFile = c.Key
			} else if positionalCount == 1 {
				expectValue = c.Key
			}
			positionalCount++
			continue
		}
		switch strings.ToLower(c.Key) {
		case "query":
			queryFile = *c.Value
		case "expect":
			expectValue = *c.Value
		}
	}

	if queryFile == "" {
		return fmt.Errorf("Perform -> postgres requires a Query clause")
	}

	if _, err := ReadPlanFixture(testDir, suiteDir, queryFile); err != nil {
		return fmt.Errorf("failed to read query fixture %s: %w", queryFile, err)
	}

	if expectValue == "" {
		return nil
	}

	if filepath.Ext(expectValue) != "" {
		if _, err := ReadPlanFixture(testDir, suiteDir, expectValue); err != nil {
			return fmt.Errorf("failed to read expect fixture %s: %w", expectValue, err)
		}
		return nil
	}

	if _, err := strconv.Atoi(expectValue); err != nil {
		return fmt.Errorf("invalid Expect value %q: must be a number or a .json file path", expectValue)
	}

	return nil
}
