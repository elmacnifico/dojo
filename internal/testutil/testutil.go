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
