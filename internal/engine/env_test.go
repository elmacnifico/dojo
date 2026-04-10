package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseEnvFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "# comment\n\nFOO=bar\nBAZ=qux\nNO_EQUALS\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	lines, err := parseEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "FOO=bar" || lines[1] != "BAZ=qux" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

func TestParseEnvFile_Missing(t *testing.T) {
	t.Parallel()
	_, err := parseEnvFile("/nonexistent/.env")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestExpandEnvVars(t *testing.T) {
	t.Parallel()
	env := []string{
		"API_GEMINI_URL=http://localhost:9000/gemini",
		"API_POSTGRES_URL=postgres://localhost:5432/db",
	}

	got := expandEnvVars("GEMINI_BASE_URL=$API_GEMINI_URL", env)
	want := "GEMINI_BASE_URL=http://localhost:9000/gemini"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExpandEnvVars_NoRef(t *testing.T) {
	t.Parallel()
	got := expandEnvVars("FOO=literal_value", nil)
	if got != "FOO=literal_value" {
		t.Errorf("got %q", got)
	}
}

func TestExpandEnvVars_MissingVar(t *testing.T) {
	t.Parallel()
	got := expandEnvVars("X=$MISSING", nil)
	if got != "X=" {
		t.Errorf("got %q, want %q", got, "X=")
	}
}

func TestSetEnv_Update(t *testing.T) {
	t.Parallel()
	env := []string{"A=1", "B=2"}
	env = setEnv(env, "A=99")
	if len(env) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(env))
	}
	if env[0] != "A=99" {
		t.Errorf("expected A=99, got %s", env[0])
	}
}

func TestSetEnv_Append(t *testing.T) {
	t.Parallel()
	env := []string{"A=1"}
	env = setEnv(env, "B=2")
	if len(env) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(env))
	}
}

func TestLoadEnvFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	dotenv := "DB=$API_PG\nSTATIC=hello\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(dotenv), 0644); err != nil {
		t.Fatal(err)
	}
	local := "SECRET=s3cret\nSTATIC=override\n"
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte(local), 0644); err != nil {
		t.Fatal(err)
	}

	base := []string{"API_PG=postgres://host/db"}
	result := loadEnvFiles(base, dir)

	lookup := func(key string) string {
		prefix := key + "="
		for i := len(result) - 1; i >= 0; i-- {
			if len(result[i]) > len(prefix) && result[i][:len(prefix)] == prefix {
				return result[i][len(prefix):]
			}
		}
		return ""
	}

	if v := lookup("DB"); v != "postgres://host/db" {
		t.Errorf("DB: got %q", v)
	}
	if v := lookup("STATIC"); v != "override" {
		t.Errorf("STATIC: got %q (expected .env.local override)", v)
	}
	if v := lookup("SECRET"); v != "s3cret" {
		t.Errorf("SECRET: got %q", v)
	}
	if v := lookup("API_PG"); v != "postgres://host/db" {
		t.Errorf("API_PG: got %q (base should be preserved)", v)
	}
}

func TestLoadEnvFiles_NoFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	base := []string{"X=1"}
	result := loadEnvFiles(base, dir)
	if len(result) != 1 || result[0] != "X=1" {
		t.Errorf("expected base unchanged, got %v", result)
	}
}
