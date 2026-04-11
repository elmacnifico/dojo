package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSuiteEnvFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Write .env
	envContent := "TEST_API_URL=http://localhost:8080\nTEST_SECRET=12345"
	os.WriteFile(filepath.Join(tmpDir, ".env"), []byte(envContent), 0644)

	// Write .env.local
	envLocalContent := "TEST_SECRET=67890\nTEST_LOCAL_ONLY=true"
	os.WriteFile(filepath.Join(tmpDir, ".env.local"), []byte(envLocalContent), 0644)

	LoadSuiteEnvFiles(tmpDir)

	if val := os.Getenv("TEST_API_URL"); val != "http://localhost:8080" {
		t.Errorf("expected TEST_API_URL to be 'http://localhost:8080', got %q", val)
	}
	if val := os.Getenv("TEST_SECRET"); val != "67890" {
		t.Errorf("expected TEST_SECRET to be '67890', got %q", val)
	}
	if val := os.Getenv("TEST_LOCAL_ONLY"); val != "true" {
		t.Errorf("expected TEST_LOCAL_ONLY to be 'true', got %q", val)
	}
}

func TestLoadSuiteEnvFiles_MissingFiles(t *testing.T) {
	tmpDir := t.TempDir()
	LoadSuiteEnvFiles(tmpDir)
	// Should not panic
}



func TestPrefixWriter(t *testing.T) {
	var buf bytes.Buffer
	pw := newPrefixWriter(&buf, "[SUT] ")

	// Write single line without newline
	n, err := pw.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("write 1 failed: %v, %d", err, n)
	}
	if buf.String() != "[SUT] hello" {
		t.Errorf("expected '[SUT] hello', got %q", buf.String())
	}

	// Write newline
	n, err = pw.Write([]byte("\n"))
	if err != nil || n != 1 {
		t.Fatalf("write 2 failed: %v, %d", err, n)
	}
	if buf.String() != "[SUT] hello\n" {
		t.Errorf("expected '[SUT] hello\\n', got %q", buf.String())
	}

	// Write new line
	n, err = pw.Write([]byte("world\n"))
	if err != nil || n != 6 {
		t.Fatalf("write 3 failed: %v, %d", err, n)
	}
	if buf.String() != "[SUT] hello\n[SUT] world\n" {
		t.Errorf("expected '[SUT] hello\\n[SUT] world\\n', got %q", buf.String())
	}

	// Write multiple lines at once
	n, err = pw.Write([]byte("line1\nline2\nline3"))
	if err != nil || n != 17 {
		t.Fatalf("write 4 failed: %v, %d", err, n)
	}
	expected := "[SUT] hello\n[SUT] world\n[SUT] line1\n[SUT] line2\n[SUT] line3"
	if buf.String() != expected {
		t.Errorf("expected %q, got %q", expected, buf.String())
	}
}
