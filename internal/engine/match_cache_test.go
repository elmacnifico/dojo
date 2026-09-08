package engine

import (
	"testing"

	"github.com/elmacnifico/dojo/internal/workspace"
)

// TestPreparedExpectedSQLCache asserts that repeated matches against the same
// expected SQL fixture produce identical results whether or not the cache is
// warm, and that distinct payloads never collide through the cache.
func TestPreparedExpectedSQLCache(t *testing.T) {
	t.Parallel()
	e := &Engine{}
	cfg := workspace.APIConfig{Protocol: "postgres", Mode: "mock"}
	vars := map[string]any{"user_id": "u1", "phone": "+15550001"}

	expected := []byte("INSERT INTO users (user_id, phone) VALUES ('{{.user_id}}', '{{.phone}}')")
	actualRendered := []byte("  INSERT INTO users (user_id, phone)   VALUES ('u1', '+15550001')  ;")

	// Warm the cache with several repeated calls (template render path).
	for i := 0; i < 3; i++ {
		if !payloadsMatchCached(e, cfg, actualRendered, expected, vars) {
			t.Fatalf("call %d: expected templated SQL match", i)
		}
	}

	// Same payload without vars: no render happens, so the literal braces must
	// survive and match only identical literal text (not a rendered form).
	bare := payloadsMatchCached(e, cfg,
		[]byte("INSERT INTO users (user_id, phone) VALUES ('{{.user_id}}', '{{.phone}}')"),
		expected, nil)
	if !bare {
		t.Error("expected literal (unrendered) brace text to match itself with no vars")
	}
	renderedWithNoVars := payloadsMatchCached(e, cfg,
		[]byte("INSERT INTO users (user_id, phone) VALUES ('u1', '+15550001')"),
		expected, nil)
	if renderedWithNoVars {
		t.Error("expected rendered actual to not match unrendered fixture when vars are absent")
	}

	// Distinct fixtures must not collide.
	other := payloadsMatchCached(e, cfg,
		[]byte("DELETE FROM users WHERE phone = '+15550001'"),
		[]byte("DELETE FROM users WHERE phone = '+15550001'"), nil)
	if !other {
		t.Error("expected distinct SQL fixture to match its exact actual")
	}

	// Cache hit path must yield the same verdict as the first call.
	if !payloadsMatchCached(e, cfg, actualRendered, expected, vars) {
		t.Error("expected cache-hit call to still match")
	}

	// Two distinct fixtures plus the bare variant of the templated one.
	if len(e.sqlCache) != 3 {
		t.Errorf("expected 3 cache entries (templated+vars, bare, delete), got %d", len(e.sqlCache))
	}
}

// TestPreparedExpectedSQLCacheNormalizeSQL verifies the cached normalized form
// matches NormalizeSQL applied fresh, including quote-aware literal handling.
func TestPreparedExpectedSQLCacheNormalizeSQL(t *testing.T) {
	t.Parallel()
	e := &Engine{}
	cfg := workspace.APIConfig{Protocol: "postgres", Mode: "mock"}

	expected := []byte("SELECT * FROM t WHERE msg = 'keep   inner'")
	actual := []byte("SELECT * FROM t  WHERE msg = 'keep   inner'")

	if !payloadsMatchCached(e, cfg, actual, expected, nil) {
		t.Fatal("expected literal-preserving match")
	}

	want := workspace.NormalizeSQL(string(expected))
	got := e.sqlCache[sqlCacheKey{payload: string(expected), vars: varsKey(nil)}]
	if got == nil {
		t.Fatal("expected cache entry after match")
	}
	if got.norm != want {
		t.Errorf("cached norm %q != fresh NormalizeSQL %q", got.norm, want)
	}
}
