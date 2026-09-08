package engine

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/elmacnifico/dojo/internal/workspace"
)

// sqlCacheKey identifies a prepared expected-SQL entry. vars carries the
// fingerprint of the ActiveTest variables map (see [varsKey]); it is 0 for the
// no-variables case.
type sqlCacheKey struct {
	payload string
	vars    string
}

// sqlCacheValue holds the prepared form of an expected SQL fixture. tmpl is
// the parsed template when the fixture contains variables; norm is the
// normalized expected SQL to contain-match against (rendered when vars are
// present, plain otherwise). One of them is used depending on vars.
type sqlCacheValue struct {
	tmpl *template.Template
	norm string
}

// sqlCacheKeyFor returns the cache key for an expected payload and variables
// map. The variables map is immutable after ActiveTest construction, so the
// rendered result is stable per key.
func sqlCacheKeyFor(payload []byte, vars map[string]any) sqlCacheKey {
	return sqlCacheKey{payload: string(payload), vars: varsKey(vars)}
}

// varsKey builds a stable fingerprint of a variables map by serializing its
// sorted entries. Tests use small maps, so this is cheap compared to the
// template parse it saves.
func varsKey(vars map[string]any) string {
	if len(vars) == 0 {
		return ""
	}
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%v;", k, vars[k])
	}
	return b.String()
}

// payloadsMatchCached is [payloadsMatch] with memoization of the expected-SQL
// preparation (template parse + render + normalize). The actual payload is
// still normalized on every call. All engine call sites invoke it under
// [Engine.processRequestMu], so the map needs no additional locking.
func payloadsMatchCached(e *Engine, cfg workspace.APIConfig, actual, expected []byte, vars map[string]any) bool {
	if workspace.APIProtocolForMatch(cfg) != "postgres" {
		return workspace.JSONSubsetMatch(actual, expected)
	}

	key := sqlCacheKeyFor(expected, vars)
	val, ok := e.sqlCache[key]
	if !ok {
		if e.sqlCache == nil {
			e.sqlCache = make(map[sqlCacheKey]*sqlCacheValue)
		}
		val = &sqlCacheValue{}
		expectedStr := string(expected)
		if len(vars) > 0 {
			if tmpl, err := template.New("sql").Parse(expectedStr); err == nil {
				val.tmpl = tmpl
			}
		}
		val.norm = workspace.NormalizeSQL(expectedStr)
		e.sqlCache[key] = val
	}

	expectedNorm := val.norm
	if len(vars) > 0 && val.tmpl != nil {
		var buf bytes.Buffer
		if err := val.tmpl.Execute(&buf, vars); err == nil {
			expectedNorm = workspace.NormalizeSQL(buf.String())
		}
	}

	// Containment on normalized SQL: the normalized expected statement must
	// appear within the normalized actual statement. Whitespace inside string
	// literals is significant (never collapsed), so 'foo  bar' cannot match
	// 'foo bar' even under containment.
	na := workspace.NormalizeSQL(string(actual))
	return strings.Contains(na, expectedNorm)
}
