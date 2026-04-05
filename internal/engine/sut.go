package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// NewCommand creates a context-aware exec.Cmd for testing utilities.
func NewCommand(ctx context.Context, name string, arg ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, arg...)
}

// SUTRunner handles launching and monitoring the Software Under Test.
type SUTRunner struct {
	binaryPath string
	workDir    string
	Env        []string
}

// NewSUTRunner initializes a runner for the SUT binary.
func NewSUTRunner(binaryPath, workDir string) *SUTRunner {
	return &SUTRunner{
		binaryPath: binaryPath,
		workDir:    workDir,
	}
}

// SUTResult holds the outcome and captured logs of a SUT run.
type SUTResult struct {
	ExitCode   int
	Output     string
	CrashEvent bool
}

// Run executes the SUT, injecting the configured environment variables.
func (r *SUTRunner) Run(ctx context.Context) (SUTResult, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", r.binaryPath)
	cmd.Env = r.Env
	if r.workDir != "" {
		cmd.Dir = r.workDir
	}

	// Create a new process group so we can cleanly kill the entire SUT process tree
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Kill the entire process group, not just the parent shell
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	var outBuf bytes.Buffer
	// Capture both stdout and stderr
	cmd.Stdout = io.MultiWriter(&outBuf, os.Stdout)
	cmd.Stderr = io.MultiWriter(&outBuf, os.Stderr)

	err := cmd.Run()
	output := outBuf.String()

	result := SUTResult{
		Output: output,
	}

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			result.CrashEvent = true
			return result, fmt.Errorf("SUT crashed with exit code %d: %w", result.ExitCode, err)
		}

		// Some other execution error (e.g. context canceled)
		result.CrashEvent = true
		return result, fmt.Errorf("SUT failed to run: %w", err)
	}

	result.ExitCode = 0
	result.CrashEvent = false

	return result, nil
}
