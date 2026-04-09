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
	"time"
)

// NewCommand creates a context-aware exec.Cmd for testing utilities.
func NewCommand(ctx context.Context, name string, arg ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, arg...)
}

// SUTRunner handles launching and monitoring the Software Under Test.
type SUTRunner struct {
	binaryPath    string
	workDir       string
	Env           []string
	ShutdownGrace time.Duration
	Verbose       bool
}

// NewSUTRunner initializes a runner for the SUT binary.
func NewSUTRunner(binaryPath, workDir string) *SUTRunner {
	return &SUTRunner{
		binaryPath:    binaryPath,
		workDir:       workDir,
		ShutdownGrace: 5 * time.Second,
	}
}

// SUTResult holds the outcome and captured logs of a SUT run.
type SUTResult struct {
	ExitCode   int
	Output     string
	CrashEvent bool
}

// prefixWriter wraps an [io.Writer] and prepends a fixed prefix to each line.
type prefixWriter struct {
	w      io.Writer
	prefix []byte
	atBOL  bool // at beginning of line
}

func newPrefixWriter(w io.Writer, prefix string) *prefixWriter {
	return &prefixWriter{w: w, prefix: []byte(prefix), atBOL: true}
}

func (pw *prefixWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		if pw.atBOL {
			if _, err := pw.w.Write(pw.prefix); err != nil {
				return written, err
			}
			pw.atBOL = false
		}
		idx := bytes.IndexByte(p, '\n')
		if idx < 0 {
			n, err := pw.w.Write(p)
			written += n
			return written, err
		}
		n, err := pw.w.Write(p[:idx+1])
		written += n
		if err != nil {
			return written, err
		}
		p = p[idx+1:]
		pw.atBOL = true
	}
	return written, nil
}

// Run executes the SUT, injecting the configured environment variables.
func (r *SUTRunner) Run(ctx context.Context) (SUTResult, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", r.binaryPath)
	cmd.Env = r.Env
	if r.workDir != "" {
		cmd.Dir = r.workDir
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = r.ShutdownGrace

	var outBuf bytes.Buffer
	if r.Verbose {
		pw := newPrefixWriter(os.Stderr, "  | ")
		cmd.Stdout = io.MultiWriter(&outBuf, pw)
		cmd.Stderr = io.MultiWriter(&outBuf, pw)
	} else {
		cmd.Stdout = &outBuf
		cmd.Stderr = &outBuf
	}

	err := cmd.Run()
	output := outBuf.String()

	result := SUTResult{
		Output: output,
	}

	if err != nil {
		if ctx.Err() != nil {
			result.ExitCode = -1
			return result, nil
		}

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			result.CrashEvent = true
			return result, fmt.Errorf("SUT crashed with exit code %d: %w", result.ExitCode, err)
		}

		result.CrashEvent = true
		return result, fmt.Errorf("SUT failed to run: %w", err)
	}

	result.ExitCode = 0
	result.CrashEvent = false

	return result, nil
}
