package engine

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckSeedRequiresLiveDB(t *testing.T) {
	t.Parallel()
	e := &Engine{log: slog.Default()}

	t.Run("nonexistent dir returns nil", func(t *testing.T) {
		t.Parallel()
		err := e.checkSeedRequiresLiveDB("/no/such/path", false)
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("sql file without live db returns error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "schema.sql"), []byte("CREATE TABLE x();"), 0644); err != nil {
			t.Fatal(err)
		}
		err := e.checkSeedRequiresLiveDB(dir, false)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "no live Postgres") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("sql file with live db returns nil", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "schema.sql"), []byte("CREATE TABLE x();"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := e.checkSeedRequiresLiveDB(dir, true); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("non-sql files only returns nil", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hi"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := e.checkSeedRequiresLiveDB(dir, false); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("subdirectories are ignored", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "backup.sql"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := e.checkSeedRequiresLiveDB(dir, false); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("readdir error on regular file", func(t *testing.T) {
		t.Parallel()
		f := filepath.Join(t.TempDir(), "notadir")
		if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		err := e.checkSeedRequiresLiveDB(f, false)
		if err == nil {
			t.Fatal("expected error for non-directory path")
		}
	})
}

func TestHttpPayloadContains(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		actual   string
		expected string
		want     bool
	}{
		{"exact match", `{"a":"b"}`, `{"a":"b"}`, true},
		{"whitespace stripped match", `{ "a" : "b" }`, `{"a":"b"}`, true},
		{"newline stripped match", "{\"a\":\n\"b\"}", `{"a":"b"}`, true},
		{"no match", `{"a":"b"}`, `{"x":"y"}`, false},
		{"empty expected", `{"a":"b"}`, ``, true},
		{"empty actual", ``, `{"a":"b"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := httpPayloadContains([]byte(tc.actual), []byte(tc.expected))
			if got != tc.want {
				t.Errorf("httpPayloadContains(%q, %q) = %v, want %v", tc.actual, tc.expected, got, tc.want)
			}
		})
	}
}
