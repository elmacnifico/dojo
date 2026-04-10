package engine

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	suiteDir      string
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
// It loads .env and .env.local from the suite directory (if they exist),
// expanding $VAR references against the already-injected env vars.
func (r *SUTRunner) Run(ctx context.Context) (SUTResult, error) {
	env := r.Env
	if r.suiteDir != "" {
		env = loadEnvFiles(env, r.suiteDir)
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", r.binaryPath)
	cmd.Env = env
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

// LoadSuiteEnvFiles loads .env and .env.local from suiteDir into the process
// environment (os.Setenv). Call before workspace loading so that os.ExpandEnv
// in API configs and os.Getenv for evaluator keys both see the values.
func LoadSuiteEnvFiles(suiteDir string) {
	env := os.Environ()
	for _, name := range []string{".env", ".env.local"} {
		path := filepath.Join(suiteDir, name)
		parsed, err := parseEnvFile(path)
		if err != nil {
			continue
		}
		for _, kv := range parsed {
			expanded := expandEnvVars(kv, env)
			env = setEnv(env, expanded)
			if idx := strings.IndexByte(expanded, '='); idx >= 0 {
				os.Setenv(expanded[:idx], expanded[idx+1:])
			}
		}
	}
}

// loadEnvFiles reads .env and .env.local from dir (if they exist) and appends
// parsed KEY=VALUE pairs to base. Values support $VAR expansion against the
// combined environment built so far.
func loadEnvFiles(base []string, dir string) []string {
	env := append([]string(nil), base...)
	for _, name := range []string{".env", ".env.local"} {
		path := filepath.Join(dir, name)
		parsed, err := parseEnvFile(path)
		if err != nil {
			continue
		}
		for _, kv := range parsed {
			expanded := expandEnvVars(kv, env)
			env = setEnv(env, expanded)
		}
	}
	return env
}

// parseEnvFile reads a file and returns KEY=VALUE lines, skipping comments
// and blank lines. Inline comments after values are not stripped.
func parseEnvFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "=") {
			continue
		}
		lines = append(lines, line)
	}
	return lines, scanner.Err()
}

// expandEnvVars replaces $VAR references in a KEY=VALUE string with values
// from the env slice. Only the value portion (after the first '=') is expanded.
func expandEnvVars(kv string, env []string) string {
	idx := strings.IndexByte(kv, '=')
	if idx < 0 {
		return kv
	}
	key := kv[:idx]
	val := kv[idx+1:]
	val = os.Expand(val, func(name string) string {
		prefix := name + "="
		for i := len(env) - 1; i >= 0; i-- {
			if strings.HasPrefix(env[i], prefix) {
				return env[i][len(prefix):]
			}
		}
		return ""
	})
	return key + "=" + val
}

// setEnv updates or appends a KEY=VALUE in the env slice.
func setEnv(env []string, kv string) []string {
	key := kv[:strings.IndexByte(kv, '=')+1]
	for i, e := range env {
		if strings.HasPrefix(e, key) {
			env[i] = kv
			return env
		}
	}
	return append(env, kv)
}
