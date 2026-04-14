package workspace

import (
	"strings"
	"testing"

	"github.com/elmacnifico/dojo/internal/testutil"
)

// --- ValidateUniqueSeedKeys tests ---

func TestValidateUniqueSeedKeys_DuplicateID(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "test_a/seed/seed.sql",
		"DELETE FROM users WHERE id = 123;\nINSERT INTO users (id, name) VALUES (123, 'alice');")
	testutil.CreateFile(t, tmp, "test_b/seed/seed.sql",
		"DELETE FROM users WHERE id = 123;\nINSERT INTO users (id, name) VALUES (123, 'bob');")

	tests := map[string]*Test{
		"test_a": {},
		"test_b": {},
	}
	err := ValidateUniqueSeedKeys(tmp, tests)
	if err == nil {
		t.Fatal("expected error for duplicate seed key")
	}
	if !strings.Contains(err.Error(), "test_a") || !strings.Contains(err.Error(), "test_b") {
		t.Fatalf("error should mention both tests: %v", err)
	}
	if !strings.Contains(err.Error(), "users") {
		t.Fatalf("error should mention table name: %v", err)
	}
}

func TestValidateUniqueSeedKeys_UniqueIDs(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "test_a/seed/seed.sql",
		"DELETE FROM users WHERE id = 100;\nINSERT INTO users (id, name) VALUES (100, 'alice');")
	testutil.CreateFile(t, tmp, "test_b/seed/seed.sql",
		"DELETE FROM users WHERE id = 200;\nINSERT INTO users (id, name) VALUES (200, 'bob');")

	tests := map[string]*Test{
		"test_a": {},
		"test_b": {},
	}
	if err := ValidateUniqueSeedKeys(tmp, tests); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateUniqueSeedKeys_MissingSeedDir(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	tests := map[string]*Test{
		"test_a": {},
	}
	if err := ValidateUniqueSeedKeys(tmp, tests); err != nil {
		t.Fatalf("missing seed dir should be OK: %v", err)
	}
}

func TestValidateUniqueSeedKeys_DifferentTablesSameValue(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "test_a/seed/seed.sql",
		"INSERT INTO users (id, name) VALUES (100, 'alice');")
	testutil.CreateFile(t, tmp, "test_b/seed/seed.sql",
		"INSERT INTO orders (id, total) VALUES (100, 50);")

	tests := map[string]*Test{
		"test_a": {},
		"test_b": {},
	}
	if err := ValidateUniqueSeedKeys(tmp, tests); err != nil {
		t.Fatalf("same value in different tables should be OK: %v", err)
	}
}

func TestValidateUniqueSeedKeys_MultipleInsertsSameTest(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "test_a/seed/seed.sql",
		"INSERT INTO users (id, name) VALUES (100, 'alice');\nINSERT INTO orders (id, total) VALUES (100, 50);")
	testutil.CreateFile(t, tmp, "test_b/seed/seed.sql",
		"INSERT INTO users (id, name) VALUES (200, 'bob');")

	tests := map[string]*Test{
		"test_a": {},
		"test_b": {},
	}
	if err := ValidateUniqueSeedKeys(tmp, tests); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateUniqueSeedKeys_QuotedValues(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "test_a/seed/seed.sql",
		"DELETE FROM users WHERE id = '100';\nINSERT INTO users (id, name) VALUES ('100', 'alice');")
	testutil.CreateFile(t, tmp, "test_b/seed/seed.sql",
		"DELETE FROM users WHERE id = '100';\nINSERT INTO users (id, name) VALUES ('100', 'bob');")

	tests := map[string]*Test{
		"test_a": {},
		"test_b": {},
	}
	err := ValidateUniqueSeedKeys(tmp, tests)
	if err == nil {
		t.Fatal("expected error for duplicate quoted seed key")
	}
}

func TestValidateUniqueSeedKeys_NowFirstValueNoFalseCollision(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "test_a/seed/seed.sql",
		"INSERT INTO users (created_at, id, name) VALUES (NOW(), 1, 'alice');")
	testutil.CreateFile(t, tmp, "test_b/seed/seed.sql",
		"INSERT INTO users (created_at, id, name) VALUES (NOW(), 2, 'bob');")

	tests := map[string]*Test{
		"test_a": {},
		"test_b": {},
	}
	if err := ValidateUniqueSeedKeys(tmp, tests); err != nil {
		t.Fatalf("NOW() as first VALUES token should not cause false collision: %v", err)
	}
}

func TestValidateUniqueSeedKeys_NestedNowExpressionNoFalseCollision(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "test_a/seed/seed.sql",
		"INSERT INTO users (created_at, id, name) VALUES ((NOW()::date + '10:00:00'::time), 1, 'a');")
	testutil.CreateFile(t, tmp, "test_b/seed/seed.sql",
		"INSERT INTO users (created_at, id, name) VALUES ((NOW()::date + '11:00:00'::time), 2, 'b');")

	tests := map[string]*Test{
		"test_a": {},
		"test_b": {},
	}
	if err := ValidateUniqueSeedKeys(tmp, tests); err != nil {
		t.Fatalf("nested NOW() expression should not cause false collision: %v", err)
	}
}

func TestValidateUniqueSeedKeys_PlainInsertDuplicateStillErrors(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "test_a/seed/seed.sql",
		"INSERT INTO users (id, name) VALUES (999, 'alice');")
	testutil.CreateFile(t, tmp, "test_b/seed/seed.sql",
		"INSERT INTO users (id, name) VALUES (999, 'bob');")

	tests := map[string]*Test{
		"test_a": {},
		"test_b": {},
	}
	err := ValidateUniqueSeedKeys(tmp, tests)
	if err == nil {
		t.Fatal("expected error for duplicate plain INSERT id across tests")
	}
	if !strings.Contains(err.Error(), "users") {
		t.Fatalf("error should mention table: %v", err)
	}
}

func TestValidateUniqueSeedKeys_KcalFirstColumnNoFalseCollision(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "test_a/seed/seed.sql",
		"INSERT INTO nutrition_logs (kcal, protein_g, user_id) VALUES ('600', '40', 1);")
	testutil.CreateFile(t, tmp, "test_b/seed/seed.sql",
		"INSERT INTO nutrition_logs (kcal, protein_g, user_id) VALUES ('600', '40', 2);")

	tests := map[string]*Test{
		"test_a": {},
		"test_b": {},
	}
	if err := ValidateUniqueSeedKeys(tmp, tests); err != nil {
		t.Fatalf("shared kcal first column should not imply duplicate key: %v", err)
	}
}

func TestValidateUniqueSeedKeys_DuplicateUserIDAcrossTestsStillErrors(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "test_a/seed/seed.sql",
		"INSERT INTO memory_logs (user_id, source, payload, ephemeral) VALUES (55, 'u', 'x', false);")
	testutil.CreateFile(t, tmp, "test_b/seed/seed.sql",
		"INSERT INTO memory_logs (user_id, source, payload, ephemeral) VALUES (55, 'u', 'y', false);")

	tests := map[string]*Test{
		"test_a": {},
		"test_b": {},
	}
	err := ValidateUniqueSeedKeys(tmp, tests)
	if err == nil {
		t.Fatal("expected error for duplicate user_id first column across tests")
	}
	if !strings.Contains(err.Error(), "memory_logs") {
		t.Fatalf("error should mention table: %v", err)
	}
}

// --- ValidateCheckSQLScoping tests ---

func TestValidateCheckSQLScoping_UnscopedSelect(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "test_a/check.sql",
		"SELECT 1 FROM nutrition_logs LIMIT 1;")
	tests := map[string]*Test{
		"test_a": {Plan: "Perform -> POST /hook\nPerform -> postgres -> check.sql -> \"1\""},
	}
	err := ValidateCheckSQLScoping(tmp, tests)
	if err == nil {
		t.Fatal("expected error for unscoped check SQL")
	}
	if !strings.Contains(err.Error(), "test_a") {
		t.Fatalf("error should mention test name: %v", err)
	}
	if !strings.Contains(err.Error(), "check.sql") {
		t.Fatalf("error should mention file name: %v", err)
	}
}

func TestValidateCheckSQLScoping_ScopedSelect(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "test_a/check.sql",
		"SELECT 1 FROM nutrition_logs WHERE user_id = 123 LIMIT 1;")
	tests := map[string]*Test{
		"test_a": {Plan: "Perform -> POST /hook\nPerform -> postgres -> check.sql -> \"1\""},
	}
	if err := ValidateCheckSQLScoping(tmp, tests); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateCheckSQLScoping_NoPostgresPerform(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	tests := map[string]*Test{
		"test_a": {Plan: "Perform -> POST /hook\nExpect -> gemini"},
	}
	if err := ValidateCheckSQLScoping(tmp, tests); err != nil {
		t.Fatalf("no postgres perform should be OK: %v", err)
	}
}

func TestValidateCheckSQLScoping_SubqueryWithWhere(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "test_a/check.sql",
		"SELECT 1 WHERE EXISTS (SELECT 1 FROM nutrition_logs WHERE user_id = 123 LIMIT 1);")
	tests := map[string]*Test{
		"test_a": {Plan: "Perform -> POST /hook\nPerform -> postgres -> check.sql -> \"1\""},
	}
	if err := ValidateCheckSQLScoping(tmp, tests); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateCheckSQLScoping_MultiplePerformPhases(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "test_a/ok.sql",
		"SELECT 1 FROM logs WHERE id = 1;")
	testutil.CreateFile(t, tmp, "test_a/bad.sql",
		"SELECT 1 FROM logs LIMIT 1;")
	tests := map[string]*Test{
		"test_a": {Plan: "Perform -> POST /hook\nPerform -> postgres -> ok.sql -> \"1\"\nPerform -> postgres -> bad.sql -> \"1\""},
	}
	err := ValidateCheckSQLScoping(tmp, tests)
	if err == nil {
		t.Fatal("expected error for unscoped bad.sql")
	}
	if !strings.Contains(err.Error(), "bad.sql") {
		t.Fatalf("error should mention bad.sql: %v", err)
	}
}

func TestValidateCheckSQLScoping_NonSelectIsSkipped(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "test_a/cleanup.sql",
		"DELETE FROM temp_table;")
	tests := map[string]*Test{
		"test_a": {Plan: "Perform -> POST /hook\nPerform -> postgres -> cleanup.sql"},
	}
	if err := ValidateCheckSQLScoping(tmp, tests); err != nil {
		t.Fatalf("non-SELECT should be skipped: %v", err)
	}
}
