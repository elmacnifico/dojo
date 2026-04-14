package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// seedKey identifies a (table, value) tuple extracted from seed SQL.
type seedKey struct {
	table string
	value string
}

var (
	// DELETE FROM <table> WHERE <col> = <value>
	deleteWhereRe = regexp.MustCompile(`(?i)DELETE\s+FROM\s+(\w+)\s+WHERE\s+\w+\s*=\s*'?(\w+)'?`)
	// INSERT INTO <table> (<columns>) VALUES (<first_value>, ...)
	insertIntoRe = regexp.MustCompile(`(?i)INSERT\s+INTO\s+(\w+)\s*\(([^)]+)\)\s*VALUES\s*\(\s*'?([^',\s)]+)'?`)
)

// extractSeedKeys parses seed SQL and returns (table, key_value) tuples.
func extractSeedKeys(sql string) []seedKey {
	seen := make(map[seedKey]bool)
	var keys []seedKey

	add := func(table, value string) {
		table = strings.ToLower(table)
		value = strings.Trim(value, "'\"")
		k := seedKey{table: table, value: value}
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}

	for _, m := range deleteWhereRe.FindAllStringSubmatch(sql, -1) {
		add(m[1], m[2])
	}
	for _, m := range insertIntoRe.FindAllStringSubmatch(sql, -1) {
		// Only treat the first value as a concurrency key when the leading column
		// is id or user_id. Otherwise the first column is often a macro (e.g.
		// kcal) shared across unrelated tests.
		colsPart := strings.TrimSpace(m[2])
		firstCol := colsPart
		if i := strings.IndexByte(colsPart, ','); i >= 0 {
			firstCol = strings.TrimSpace(colsPart[:i])
		}
		switch strings.ToLower(firstCol) {
		case "id", "user_id":
		default:
			continue
		}
		// Expressions like NOW() truncate to the same synthetic token for
		// unrelated rows (e.g. "NOW("), causing false collisions across tests.
		v := strings.Trim(strings.TrimSpace(m[3]), "'\"")
		if v == "" || strings.ContainsAny(v, "()") {
			continue
		}
		add(m[1], m[3])
	}
	return keys
}

// ValidateUniqueSeedKeys scans seed SQL files for all tests in a suite and
// returns an error if two or more tests seed the same table with the same
// key value. Under concurrent execution, overlapping seeds corrupt each
// other's data.
func ValidateUniqueSeedKeys(suiteDir string, tests map[string]*Test) error {
	type collision struct {
		key   seedKey
		tests []string
	}

	seen := make(map[seedKey][]string) // (table, value) -> test names

	for testName := range tests {
		seedDir := filepath.Join(suiteDir, testName, "seed")
		entries, err := os.ReadDir(seedDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("test %s: reading seed dir: %w", testName, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(seedDir, entry.Name()))
			if err != nil {
				return fmt.Errorf("test %s: reading seed %s: %w", testName, entry.Name(), err)
			}
			for _, k := range extractSeedKeys(string(b)) {
				seen[k] = append(seen[k], testName)
			}
		}
	}

	var collisions []collision
	for k, names := range seen {
		unique := make(map[string]bool)
		for _, n := range names {
			unique[n] = true
		}
		if len(unique) < 2 {
			continue
		}
		sorted := make([]string, 0, len(unique))
		for n := range unique {
			sorted = append(sorted, n)
		}
		sort.Strings(sorted)
		collisions = append(collisions, collision{key: k, tests: sorted})
	}
	if len(collisions) == 0 {
		return nil
	}

	sort.Slice(collisions, func(i, j int) bool {
		if collisions[i].key.table != collisions[j].key.table {
			return collisions[i].key.table < collisions[j].key.table
		}
		return collisions[i].key.value < collisions[j].key.value
	})

	var b strings.Builder
	b.WriteString("overlapping seed data detected:\n")
	for _, c := range collisions {
		fmt.Fprintf(&b, "  table %q key %q: %v\n", c.key.table, c.key.value, c.tests)
	}
	return fmt.Errorf("%s", b.String())
}

// selectFromRe matches SELECT ... FROM <table> (case-insensitive).
var selectFromRe = regexp.MustCompile(`(?i)\bSELECT\b.*\bFROM\b`)

// whereRe matches the WHERE keyword (case-insensitive).
var whereRe = regexp.MustCompile(`(?i)\bWHERE\b`)

// isSQLUnscoped returns true if the SQL contains a SELECT...FROM but no WHERE clause.
func isSQLUnscoped(sql string) bool {
	if !selectFromRe.MatchString(sql) {
		return false
	}
	return !whereRe.MatchString(sql)
}

// ValidateCheckSQLScoping inspects every Perform -> postgres SQL file in the
// suite and returns an error if any SELECT query lacks a WHERE clause. Under
// concurrent execution, unscoped queries can match rows from other tests.
func ValidateCheckSQLScoping(suiteDir string, tests map[string]*Test) error {
	type violation struct {
		testName string
		file     string
	}
	var violations []violation

	for testName, test := range tests {
		doc, err := ParsePlan(test.Plan)
		if err != nil {
			continue
		}
		phases := SplitPlanPhases(doc.Lines)
		testDir := filepath.Join(suiteDir, testName)

		for i, ph := range phases {
			if i == 0 {
				continue
			}
			if !strings.EqualFold(ph.Perform.Target, "postgres") {
				continue
			}

			queryFile := extractQueryFile(ph.Perform)
			if queryFile == "" {
				continue
			}

			b, err := ReadPlanFixture(testDir, suiteDir, queryFile)
			if err != nil {
				continue
			}

			if isSQLUnscoped(string(b)) {
				violations = append(violations, violation{testName: testName, file: queryFile})
			}
		}
	}

	if len(violations) == 0 {
		return nil
	}

	sort.Slice(violations, func(i, j int) bool {
		if violations[i].testName != violations[j].testName {
			return violations[i].testName < violations[j].testName
		}
		return violations[i].file < violations[j].file
	})

	var b strings.Builder
	b.WriteString("unscoped check SQL (SELECT without WHERE) detected:\n")
	for _, v := range violations {
		fmt.Fprintf(&b, "  test %s: %s\n", v.testName, v.file)
	}
	return fmt.Errorf("%s", b.String())
}

// extractQueryFile pulls the query filename from a Perform -> postgres line.
func extractQueryFile(line ParsedLine) string {
	positional := 0
	for _, c := range line.Clauses {
		if c.Value == nil {
			if positional == 0 {
				return c.Key
			}
			positional++
			continue
		}
		if strings.EqualFold(c.Key, "Query") {
			return *c.Value
		}
	}
	return ""
}
