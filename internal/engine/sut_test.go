package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"dojo/internal/engine"
)

func TestSUTRunner(t *testing.T) {
	tmpDir := t.TempDir()
	mainFile := filepath.Join(tmpDir, "main.go")
	dummySUT := `package main
import (
	"fmt"
	"os"
)
func main() {
	fmt.Println("Hello from SUT")
	dbURL := os.Getenv("API_POSTGRES_URL")
	fmt.Printf("Connected to: %s\n", dbURL)
	
	if os.Getenv("CRASH") == "1" {
		os.Exit(1)
	}
}
`
	if err := os.WriteFile(mainFile, []byte(dummySUT), 0644); err != nil {
		t.Fatalf("Failed to create dummy SUT: %v", err)
	}

	binFile := filepath.Join(tmpDir, "sut_bin")
	cmd := engine.NewCommand(context.Background(), "go", "build", "-o", binFile, mainFile)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to compile dummy SUT: %v", err)
	}

	t.Run("Success", func(t *testing.T) {
		runner := engine.NewSUTRunner(binFile, tmpDir)
		runner.Env = []string{
			"API_POSTGRES_URL=postgres://localhost:5432",
		}
		
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		result, err := runner.Run(ctx)
		if err != nil {
			t.Fatalf("Failed to run SUT: %v", err)
		}

		if result.ExitCode != 0 {
			t.Errorf("Expected exit code 0, got %d", result.ExitCode)
		}

		if !strings.Contains(result.Output, "Hello from SUT") {
			t.Errorf("Expected output to contain 'Hello from SUT', got %s", result.Output)
		}

		if !strings.Contains(result.Output, "Connected to: postgres://localhost:5432") {
			t.Errorf("Expected injected Env output, got %s", result.Output)
		}

		if result.CrashEvent {
			t.Errorf("Did not expect CrashEvent to be true")
		}
	})

	t.Run("Crash", func(t *testing.T) {
		runner := engine.NewSUTRunner(binFile, tmpDir)
		runner.Env = []string{"CRASH=1"}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		result, err := runner.Run(ctx)
		if err == nil {
			t.Errorf("Expected error from crashing SUT")
		}

		if result.ExitCode == 0 {
			t.Errorf("Expected non-zero exit code, got %d", result.ExitCode)
		}

		if !result.CrashEvent {
			t.Errorf("Expected CrashEvent to be true")
		}
	})
}
