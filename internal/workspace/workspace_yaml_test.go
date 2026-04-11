package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestDurationUnmarshalYAML(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		expected time.Duration
		wantErr  bool
	}{
		{"valid seconds", "5s", 5 * time.Second, false},
		{"valid ms", "300ms", 300 * time.Millisecond, false},
		{"valid minutes", "2m", 2 * time.Minute, false},
		{"empty string", "\"\"", 0, false},
		{"invalid format", "5", 0, true},
		{"not a string", "5", 0, true}, // yaml.v3 parses 5 as int if not quoted, but our unmarshal expects string
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Duration
			err := yaml.Unmarshal([]byte(tt.yaml), &d)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalYAML() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && d.Duration != tt.expected {
				t.Errorf("UnmarshalYAML() got = %v, want %v", d.Duration, tt.expected)
			}
		})
	}
}



func TestLoadYAML(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.yaml")
	
	// Valid YAML
	os.WriteFile(path, []byte("concurrency: 5"), 0644)
	var cfg DojoConfig
	err := loadYAML(path, &cfg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.Concurrency != 5 {
		t.Errorf("expected concurrency 5, got %d", cfg.Concurrency)
	}

	// Invalid YAML
	os.WriteFile(path, []byte("concurrency: ["), 0644)
	err = loadYAML(path, &cfg)
	if err == nil {
		t.Fatal("expected error for invalid yaml, got nil")
	}

	// Missing file
	err = loadYAML(filepath.Join(tmpDir, "missing.yaml"), &cfg)
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
