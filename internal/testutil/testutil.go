// Package testutil provides shared test helpers for Dojo packages.
package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// CreateFile writes content to baseDir/path, creating intermediate directories.
func CreateFile(t *testing.T, baseDir, path, content string) {
	t.Helper()
	fullPath := filepath.Join(baseDir, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		t.Fatalf("Failed to create dirs for %s: %v", path, err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write file %s: %v", path, err)
	}
}

// AppendFile appends content to a file, creating it if it doesn't exist.
func AppendFile(t *testing.T, baseDir, relPath, content string) {
	t.Helper()
	fullPath := filepath.Join(baseDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		t.Fatalf("failed to create dir for %s: %v", relPath, err)
	}
	f, err := os.OpenFile(fullPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("failed to open file %s: %v", relPath, err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("failed to write to file %s: %v", relPath, err)
	}
}
