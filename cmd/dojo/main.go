package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"dojo/internal/engine"
	"dojo/internal/proxy"
	"dojo/internal/workspace"
)

func main() {
	helpFlag := flag.Bool("help", false, "Show help message")
	hFlag := flag.Bool("h", false, "Show help message")

	flag.Usage = func() {
		fmt.Fprintf(os.Stdout, "Dojo: The Universal Black-Box Contract Engine\n\n")
		fmt.Fprintf(os.Stdout, "Usage:\n")
		fmt.Fprintf(os.Stdout, "  dojo [flags] <suite_directory>\n\n")
		fmt.Fprintf(os.Stdout, "Flags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stdout, "\nExample:\n")
		fmt.Fprintf(os.Stdout, "  dojo run ./example/tests/blackbox\n")
		fmt.Fprintf(os.Stdout, "  dojo ./example/tests/blackbox\n")
		fmt.Fprintf(os.Stdout, "\nRelative suite paths resolve from the current directory first; if missing,\n")
		fmt.Fprintf(os.Stdout, "from the Go module root (directory containing go.mod).\n")
	}

	flag.Parse()

	if *helpFlag || *hFlag {
		flag.Usage()
		os.Exit(0)
	}

	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Error: Missing suite directory.\n\n")
		flag.Usage()
		os.Exit(1)
	}

	suitePath := args[0]
	if args[0] == "run" && len(args) > 1 {
		suitePath = args[1]
	}

	suiteDir, err := resolveSuiteDirectory(suitePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid suite path %q: %v\n", suitePath, err)
		os.Exit(1)
	}
	workspaceDir := filepath.Dir(suiteDir)
	suiteName := filepath.Base(suiteDir)

	fmt.Printf("Starting Dojo Engine...\n")
	fmt.Printf("Workspace Root: %s\n", workspaceDir)
	fmt.Printf("Target Suite:   %s\n\n", suiteName)

	ws, err := workspace.LoadWorkspace(workspaceDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load workspace: %v\n", err)
		os.Exit(1)
	}

	suite, ok := ws.Suites[suiteName]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: Suite '%s' not found in workspace '%s'\n", suiteName, workspaceDir)
		os.Exit(1)
	}

	fmt.Printf("Loaded Suite '%s' successfully.\n", suiteName)
	fmt.Printf("  Concurrency: %d\n", suite.Config.Concurrency)
	fmt.Printf("  Tests Found: %d\n", len(suite.Tests))

	fmt.Println("\nBooting Engine and Starting Proxies...")
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	eng := engine.NewEngine(ws, engine.WithLogger(logger))

	// Register generic Adapters
	eng.RegisterAdapter(proxy.NewHTTPInitiator())

	if err := eng.StartProxies(context.Background(), suiteName); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start proxies: %v\n", err)
		os.Exit(1)
	}
	defer eng.StopProxies()

	fmt.Printf("Engine running.\n")
	fmt.Println("\nValidating Suite and Pre-flight Checks...")

	summary, err := eng.RunSuite(context.Background(), suiteName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nFatal Error during Suite Execution: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n--- SUITE EXECUTION COMPLETE ---\n")
	fmt.Printf("Total Tests: %d\n", summary.TotalRuns)
	fmt.Printf("Passed:      %d\n", summary.Passed)
	fmt.Printf("Failed:      %d\n", summary.Failed)

	if summary.Failed > 0 {
		for _, f := range summary.Failures {
			fmt.Printf("  ❌ %s: %s\n", f.TestName, f.Reason)
		}
		os.Exit(1)
	} else {
		fmt.Println("  ✅ All tests passed.")
	}
}

// findModuleRoot walks dir and parents until a directory containing go.mod is found.
func findModuleRoot(dir string) (string, error) {
	for {
		st, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err == nil && !st.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

// resolveSuiteDirectory turns a user-provided suite path into an absolute directory.
// Relative paths are resolved against the current working directory first; if that
// path does not exist, they are resolved against the Go module root (directory with
// go.mod), so `go run ./cmd/dojo/main.go example/tests/blackbox` works from any cwd
// under the module.
func resolveSuiteDirectory(suitePath string) (string, error) {
	clean := filepath.Clean(suitePath)
	if filepath.IsAbs(clean) {
		st, err := os.Stat(clean)
		if err != nil {
			return "", err
		}
		if !st.IsDir() {
			return "", fmt.Errorf("not a directory: %s", clean)
		}
		return clean, nil
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	fromWD := filepath.Join(wd, clean)
	if st, err := os.Stat(fromWD); err == nil && st.IsDir() {
		abs, err := filepath.Abs(fromWD)
		if err != nil {
			return "", err
		}
		return abs, nil
	}

	modRoot, err := findModuleRoot(wd)
	if err != nil {
		return "", fmt.Errorf("suite %q not under %s and no module root: %w", suitePath, wd, err)
	}
	fromMod := filepath.Join(modRoot, clean)
	stMod, err := os.Stat(fromMod)
	if err != nil {
		return "", fmt.Errorf("suite directory not found (tried %s and module root %s): %w", fromWD, modRoot, err)
	}
	if !stMod.IsDir() {
		return "", fmt.Errorf("not a directory: %s", fromMod)
	}
	return filepath.Abs(fromMod)
}
