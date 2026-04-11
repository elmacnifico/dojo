//go:build integration

package main

import (
	"bytes"
	"net"
	"os/exec"
	"path/filepath"
	"testing"
)

// Example blackbox SUT binds :8080; skip when something else already holds it
// (e.g. a leaked SUT from a prior failed run before StopProxies ran on every path).
func tcpListenFree(addr string) bool {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func TestDojoCLI_ExampleSuite(t *testing.T) {
	if !tcpListenFree("127.0.0.1:8080") {
		t.Skip("127.0.0.1:8080 busy (example SUT needs this port)")
	}

	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "dojo")

	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	buildCmd.Dir = "."
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build CLI: %v\n%s", err, output)
	}

	runCmd := exec.Command(binPath, "../../example/tests/blackbox")
	runCmd.Dir = "."

	var out bytes.Buffer
	runCmd.Stdout = &out
	runCmd.Stderr = &out

	if err := runCmd.Run(); err != nil {
		t.Fatalf("CLI run failed: %v\n%s", err, out.String())
	}

	if !bytes.Contains(out.Bytes(), []byte("All tests passed.")) {
		t.Errorf("expected output to contain 'All tests passed.', got:\n%s", out.String())
	}
}
