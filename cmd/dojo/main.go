package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"

	"dojo/internal/engine"
	"dojo/internal/reporter"
	"dojo/internal/workspace"
)

func main() {
	helpFlag := flag.Bool("help", false, "Show help message")
	hFlag := flag.Bool("h", false, "Show help message")
	outputDir := flag.String("output", "", "Write summary.json and summary.md to this directory")
	oFlag := flag.String("o", "", "Shorthand for --output")
	formatFlag := flag.String("format", "console", "Output format: console, json, or jsonl")
	verboseFlag := flag.Bool("verbose", false, "Show debug logs and SUT output")
	vFlag := flag.Bool("v", false, "Shorthand for --verbose")
	traceFlag := flag.Bool("trace", false, "Trace log HTTP and Postgres request/response payloads")
	runFilter := flag.String("run", "", "Regular expression; only run tests whose names match")

	flag.Usage = func() {
		fmt.Fprintf(os.Stdout, "Dojo: The Universal Black-Box Contract Engine\n\n")
		fmt.Fprintf(os.Stdout, "Usage:\n")
		fmt.Fprintf(os.Stdout, "  dojo [flags] <suite_directory>\n\n")
		fmt.Fprintf(os.Stdout, "Flags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stdout, "\nExample:\n")
		fmt.Fprintf(os.Stdout, "  dojo run ./example/tests/blackbox\n")
		fmt.Fprintf(os.Stdout, "  dojo ./example/tests/blackbox\n")
		fmt.Fprintf(os.Stdout, "  dojo --format json -o results/ ./example/tests/blackbox\n")
		fmt.Fprintf(os.Stdout, "  dojo -run 'test_foo|test_bar' ./my_suite\n")
		fmt.Fprintf(os.Stdout, "\nRelative suite paths resolve from the current directory first; if missing,\n")
		fmt.Fprintf(os.Stdout, "from the Go module root (directory containing go.mod).\n")
	}

	flag.Parse()

	if *helpFlag || *hFlag {
		flag.Usage()
		os.Exit(0)
	}

	verbose := *verboseFlag || *vFlag
	trace := *traceFlag
	outDir := *outputDir
	if outDir == "" {
		outDir = *oFlag
	}
	format := strings.ToLower(*formatFlag)
	switch format {
	case "console", "json", "jsonl":
	default:
		fmt.Fprintf(os.Stderr, "Error: unsupported --format %q (must be console, json, or jsonl)\n", format)
		os.Exit(1)
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	exitCode := 0
	var eng *engine.Engine
	defer func() {
		if eng != nil {
			if err := eng.StopProxies(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: proxy shutdown: %v\n", err)
			}
		}
		os.Exit(exitCode)
	}()

	if format == "console" {
		fmt.Printf("Starting Dojo Engine...\n")
		fmt.Printf("Workspace Root: %s\n", workspaceDir)
		fmt.Printf("Target Suite:   %s\n\n", suiteName)
	}

	engine.LoadSuiteEnvFiles(suiteDir)

	ws, err := workspace.LoadWorkspace(workspaceDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load workspace: %v\n", err)
		exitCode = 1
		return
	}

	suite, ok := ws.Suites[suiteName]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: Suite '%s' not found in workspace '%s'\n", suiteName, workspaceDir)
		exitCode = 1
		return
	}

	if err := workspace.PreflightLoadedSuite(ws, suiteName); err != nil {
		fmt.Fprintf(os.Stderr, "Preflight failed: %v\n", err)
		exitCode = 1
		return
	}

	if *runFilter != "" {
		re, err := regexp.Compile(*runFilter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid -run regular expression: %v\n", err)
			exitCode = 1
			return
		}
		filtered := make(map[string]*workspace.Test)
		for name, t := range suite.Tests {
			if re.MatchString(name) {
				filtered[name] = t
			}
		}
		if len(filtered) == 0 {
			fmt.Fprintf(os.Stderr, "No tests matched -run %q (suite has %d tests)\n", *runFilter, len(suite.Tests))
			exitCode = 1
			return
		}
		suite.Tests = filtered
	}

	if format == "console" {
		fmt.Printf("Loaded Suite '%s' successfully.\n", suiteName)
		fmt.Printf("  Tests:       %d\n", len(suite.Tests))
		fmt.Printf("  Concurrency: %d\n", suite.Config.Concurrency)
	}

	logLevel := slog.LevelInfo
	if verbose {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	eng = engine.NewEngine(ws, engine.WithLogger(logger), engine.WithVerbose(verbose), engine.WithTrace(trace))

	startupReport, err := eng.StartProxies(ctx, suiteName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start proxies: %v\n", err)
		exitCode = 1
		return
	}

	if format == "console" {
		fmt.Printf("\n--- RUNNING SUITE: %s (%d tests, concurrency %d) ---\n\n",
			suiteName, len(suite.Tests), suite.Config.Concurrency)
		if startupReport.Ran {
			fmt.Printf("  PASS  startup.plan  (%s)\n", formatDuration(startupReport.DurationMs))
		}
	}

	var printMu sync.Mutex
	onResult := func(tr workspace.TestResult) {
		printMu.Lock()
		defer printMu.Unlock()

		switch format {
		case "jsonl":
			b, err := json.Marshal(tr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to marshal test result: %v\n", err)
			} else {
				fmt.Println(string(b))
			}
		case "console":
			dur := formatDuration(tr.DurationMs)
			usageStr := ""
			if tr.LLMUsage != nil && (tr.LLMUsage.PromptTokens > 0 || tr.LLMUsage.CompletionTokens > 0) {
				usageStr = fmt.Sprintf(" [%d tokens]", tr.LLMUsage.TotalTokens)
			}
			if tr.Status == "pass" {
				fmt.Printf("  PASS  %s  (%s)%s\n", tr.TestName, dur, usageStr)
			} else {
				fmt.Printf("  FAIL  %s  (%s)%s: %s\n", tr.TestName, dur, usageStr, tr.Reason)
			}
		}
	}

	summary, err := eng.RunSuite(ctx, suiteName, onResult)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nFatal Error during Suite Execution: %v\n", err)
		exitCode = 1
		return
	}

	sort.Slice(summary.Failures, func(i, j int) bool {
		return summary.Failures[i].TestName < summary.Failures[j].TestName
	})
	sort.Slice(summary.Results, func(i, j int) bool {
		return summary.Results[i].TestName < summary.Results[j].TestName
	})

	switch format {
	case "json":
		b, err := json.MarshalIndent(summary, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to marshal summary: %v\n", err)
		} else {
			fmt.Println(string(b))
		}
	case "console":
		printConsoleSummary(summary)
	}

	if outDir != "" {
		r := reporter.NewReporter(outDir)
		if err := r.Generate(summary); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to write report: %v\n", err)
		} else if format == "console" {
			fmt.Printf("\nReport written to %s\n", outDir)
		}
	}

	if summary.Failed > 0 {
		exitCode = 1
	}
}

func printConsoleSummary(summary workspace.TestSummary) {
	fmt.Printf("\n--- RESULTS ---\n")
	fmt.Printf("Total: %d   Passed: %d   Failed: %d   Duration: %s\n",
		summary.TotalRuns, summary.Passed, summary.Failed,
		formatDuration(summary.DurationMs))

	if summary.Failed > 0 {
		fmt.Printf("\nFailures:\n")
		for _, f := range summary.Failures {
			fmt.Printf("  FAIL  %s: %s\n", f.TestName, f.Reason)
		}
	} else {
		fmt.Printf("\nAll tests passed.\n")
	}
}

func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
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
