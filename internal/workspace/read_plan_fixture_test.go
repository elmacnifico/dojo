package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadPlanFixture_TestDirWins(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testDir := filepath.Join(tmp, "test_x")
	suiteDir := filepath.Join(tmp, "suite_y")
	if err := os.MkdirAll(testDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(suiteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(suiteDir, "f.sql"), []byte("suite"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testDir, "f.sql"), []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := ReadPlanFixture(testDir, suiteDir, "f.sql")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "test" {
		t.Fatalf("got %q", b)
	}
}

func TestReadPlanFixture_FallbackToSuiteDir(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testDir := filepath.Join(tmp, "test_x")
	suiteDir := filepath.Join(tmp, "suite_y")
	if err := os.MkdirAll(testDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(suiteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(suiteDir, "only.sql"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := ReadPlanFixture(testDir, suiteDir, "only.sql")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "ok" {
		t.Fatalf("got %q", b)
	}
}

func TestReadPlanFixture_Missing(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	_, err := ReadPlanFixture(tmp, tmp, "nope.sql")
	if err == nil {
		t.Fatal("expected error")
	}
}
