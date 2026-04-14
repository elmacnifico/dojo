//go:build integration

package main

import (
	"bytes"
	"net"
	"os/exec"
	"path/filepath"
	"testing"
)

// Example blackbox suite binds the SUT on 127.0.0.1:29473 (see example/tests/blackbox/dojo.yaml).
// Skip when that port is busy (e.g. a leaked SUT from a prior failed run).
func tcpListenFree(addr string) bool {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func TestDojoCLI_ExampleSuite(t *testing.T) {
	if !tcpListenFree("127.0.0.1:29473") {
		t.Skip("127.0.0.1:29473 busy (example blackbox SUT needs this port)")
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
