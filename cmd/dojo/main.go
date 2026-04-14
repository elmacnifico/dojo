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

	"github.com/elmacnifico/dojo/internal/engine"
	"github.com/elmacnifico/dojo/internal/reporter"
	"github.com/elmacnifico/dojo/internal/workspace"
)

// argvHasLLMUsage reports whether raw argv contains the LLM usage flag. Needed
// because the stdlib flag parser stops at the first non-flag argument, so forms
// like `dojo run ./suite --llm-usage` never set -llm-usage via flag.Parse alone.
func argvHasLLMUsage(argv []string) bool {
	for _, a := range argv {
		switch {
		case a == "--llm-usage", a == "-llm-usage":
			return true
		case strings.HasPrefix(a, "--llm-usage="):
			v := strings.TrimPrefix(a, "--llm-usage=")
			return v != "false" && v != "0"
		case strings.HasPrefix(a, "-llm-usage="):
			v := strings.TrimPrefix(a, "-llm-usage=")
			return v != "false" && v != "0"
		}
	}
	return false
}

func isKnownBoolCLIArg(a string) bool {
	switch a {
	case "-v", "-verbose", "--verbose", "-trace", "--trace",
		"--llm-usage", "-llm-usage",
		"-h", "-help", "--help":
		return true
	default:
		return false
	}
}

// nextSuitePathFromArgs returns the suite directory from flag.Args() after
// optional `run`, skipping flag-like tokens that were not parsed because they
// appeared after the first non-flag (e.g. `dojo run --llm-usage ./suite`).
func nextSuitePathFromArgs(args []string) (string, error) {
	i := 0
	if len(args) > 0 && args[0] == "run" {
		i = 1
	}
	for i < len(args) {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			if strings.Contains(a, "=") {
				i++
				continue
			}
			if isKnownBoolCLIArg(a) {
				i++
				continue
			}
			switch a {
			case "-o", "--output", "-format", "--format", "-run", "--run":
				if i+1 >= len(args) {
					return "", fmt.Errorf("flag %s requires a value", a)
				}
				i += 2
				continue
			default:
				i++
				continue
			}
		}
		return a, nil
	}
	return "", fmt.Errorf("missing suite directory")
}

func main() {
	helpFlag := flag.Bool("help", false, "Show help message")
	hFlag := flag.Bool("h", false, "Show help message")
	outputDir := flag.String("output", "", "Write summary.json and summary.md to this directory")
	oFlag := flag.String("o", "", "Shorthand for --output")
	formatFlag := flag.String("format", "console", "Output format: console, json, or jsonl")
	verboseFlag := flag.Bool("verbose", false, "Show debug logs and SUT output")
	vFlag := flag.Bool("v", false, "Shorthand for --verbose")
	traceFlag := flag.Bool("trace", false, "Trace log HTTP and Postgres request/response payloads")
	llmUsageFlag := flag.Bool("llm-usage", false, "Console only: print per-test and suite LLM usage tables (default: no LLM lines on stdout; JSON/jsonl unchanged)")
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
		fmt.Fprintf(os.Stdout, "  dojo --llm-usage ./example/tests/blackbox\n")
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
	// Go's flag package stops at the first non-flag token, so flags after `run` or
	// after the suite path are never parsed; recover --llm-usage from raw argv.
	llmUsage := *llmUsageFlag || argvHasLLMUsage(os.Args[1:])
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

	suitePath, err := nextSuitePathFromArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		flag.Usage()
		os.Exit(1)
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
			printConsoleTestResult(tr, dur, llmUsage)
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
		printConsoleSummary(summary, llmUsage)
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

// printConsoleTestResult prints one test line; with llmVerbose, adds a full LLM block.
func printConsoleTestResult(tr workspace.TestResult, dur string, llmVerbose bool) {
	if llmVerbose {
		if tr.Status == "pass" {
			fmt.Printf("  PASS  %s  (%s)\n", tr.TestName, dur)
		} else {
			fmt.Printf("  FAIL  %s  (%s): %s\n", tr.TestName, dur, tr.Reason)
		}
		if tr.LLMUsage != nil && tr.LLMUsage.AnyUsage() {
			fmt.Println()
			printLLMUsageExpanded("      ", fmt.Sprintf("LLM (%s)", tr.TestName), tr.LLMUsage, tr.LLMUsageByAPI, tr.LLMUsageDerived)
		}
		return
	}

	if tr.Status == "pass" {
		fmt.Printf("  PASS  %s  (%s)\n", tr.TestName, dur)
	} else {
		fmt.Printf("  FAIL  %s  (%s): %s\n", tr.TestName, dur, tr.Reason)
	}
}

func sortedAPIKeysForLLM(m map[string]workspace.LLMUsage) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type llmExtraPair struct {
	name string
	val  int
}

func llmUsageExtraPairs(u *workspace.LLMUsage) []llmExtraPair {
	if u == nil {
		return nil
	}
	var out []llmExtraPair
	add := func(name string, v int) {
		if v != 0 {
			out = append(out, llmExtraPair{name: name, val: v})
		}
	}
	add("Prompt Cache", u.CachedPromptTokens)
	add("Cache Read", u.CacheReadInputTokens)
	add("Cache Creation", u.CacheCreationInputTokens)
	add("Reasoning", u.ReasoningTokens)
	add("Thoughts", u.ThoughtsTokens)
	add("Tool Use Prompt", u.ToolUsePromptTokens)
	add("Audio Prompt", u.AudioPromptTokens)
	add("Audio Completion", u.AudioCompletionTokens)
	add("Accepted Prediction", u.AcceptedPredictionTokens)
	add("Rejected Prediction", u.RejectedPredictionTokens)
	return out
}

// llmCostColumns maps raw usage into the cost-aware console breakdown:
// Input = uncached prompt; Cached = prompt-cache + cache-read input;
// Thinking = reasoning + thoughts.
//
// Output: OpenAI-style responses nest reasoning inside completion_tokens, so we
// subtract thinking from completion when ThoughtsTokens is zero. Gemini reports
// thoughtsTokenCount separately from candidatesTokenCount (disjoint tallies), so
// when ThoughtsTokens > 0 we must not subtract; billable generation on the output
// side is completion + thinking.
func llmCostColumns(ua *workspace.LLMUsage) (input, cached, output, thinking int) {
	if ua == nil {
		return 0, 0, 0, 0
	}
	thinking = ua.ReasoningTokens + ua.ThoughtsTokens
	switch {
	case ua.ThoughtsTokens > 0:
		output = ua.CompletionTokens + thinking
	case thinking > 0 && ua.CompletionTokens >= thinking:
		output = ua.CompletionTokens - thinking
	default:
		output = ua.CompletionTokens
	}
	if output < 0 {
		output = 0
	}
	cached = ua.CachedPromptTokens + ua.CacheReadInputTokens
	input = ua.PromptTokens - ua.CachedPromptTokens - ua.CacheReadInputTokens
	if input < 0 {
		input = 0
	}
	return input, cached, output, thinking
}

func printBoxTable(linePrefix string, headers []string, rows [][]string) {
	if len(headers) == 0 {
		return
	}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	var top, mid, bot strings.Builder
	top.WriteString(linePrefix + "┌")
	mid.WriteString(linePrefix + "├")
	bot.WriteString(linePrefix + "└")

	for i, w := range widths {
		dash := strings.Repeat("─", w+2)
		top.WriteString(dash)
		mid.WriteString(dash)
		bot.WriteString(dash)
		if i < len(widths)-1 {
			top.WriteString("┬")
			mid.WriteString("┼")
			bot.WriteString("┴")
		} else {
			top.WriteString("┐")
			mid.WriteString("┤")
			bot.WriteString("┘")
		}
	}
	fmt.Println(top.String())

	printRow := func(cells []string) {
		var b strings.Builder
		b.WriteString(linePrefix + "│")
		for i, cell := range cells {
			pad := widths[i] - len(cell)
			b.WriteString(" " + cell + strings.Repeat(" ", pad) + " │")
		}
		fmt.Println(b.String())
	}

	printRow(headers)
	fmt.Println(mid.String())
	for _, row := range rows {
		printRow(row)
	}
	fmt.Println(bot.String())
}

// printLLMUsageExpanded prints tabular LLM usage (per test or suite aggregate).
func printLLMUsageExpanded(linePrefix, title string, u *workspace.LLMUsage, byAPI map[string]workspace.LLMUsage, d *workspace.LLMUsageDerived) {
	if u == nil || !u.AnyUsage() {
		return
	}
	fmt.Printf("%s--- %s ---\n", linePrefix, title)

	headers := []string{"API", "Input", "Cached", "Output", "Thinking"}
	var rows [][]string

	addRow := func(name string, ua *workspace.LLMUsage) {
		if !ua.AnyUsage() {
			return
		}
		input, cached, output, thinking := llmCostColumns(ua)
		rows = append(rows, []string{
			name,
			fmt.Sprintf("%d", input),
			fmt.Sprintf("%d", cached),
			fmt.Sprintf("%d", output),
			fmt.Sprintf("%d", thinking),
		})
	}

	if len(byAPI) == 0 {
		addRow("(aggregated)", u)
	} else {
		for _, name := range sortedAPIKeysForLLM(byAPI) {
			ua := byAPI[name]
			addRow(name, &ua)
		}
	}

	printBoxTable(linePrefix, headers, rows)

	// Print extras as bullet points
	pairs := llmUsageExtraPairs(u)
	if len(pairs) > 0 {
		for _, p := range pairs {
			// Skip the ones we already included in the core table
			if p.name == "Prompt Cache" || p.name == "Cache Read" || p.name == "Reasoning" || p.name == "Thoughts" {
				continue
			}
			fmt.Printf("%s  • %s: %d\n", linePrefix, p.name, p.val)
		}
	}
	if d != nil {
		if d.PromptCacheHitRate != nil {
			fmt.Printf("%s  • Prompt Cache Hit Rate: %.1f%% (%d/%d)\n", linePrefix, *d.PromptCacheHitRate*100, d.PromptCacheHitNumerator, d.PromptCacheHitDenominator)
		}
		if d.CacheReadInputRate != nil {
			fmt.Printf("%s  • Cache Read Input Rate: %.1f%% (%d/%d)\n", linePrefix, *d.CacheReadInputRate*100, d.CacheReadInputNumerator, d.CacheReadInputDenominator)
		}
	}
}

func printConsoleSummary(summary workspace.TestSummary, llmVerbose bool) {
	fmt.Printf("\n--- RESULTS ---\n")
	fmt.Printf("Total: %d   Passed: %d   Failed: %d   Duration: %s\n",
		summary.TotalRuns, summary.Passed, summary.Failed,
		formatDuration(summary.DurationMs))

	suiteTotal, suiteByAPI := workspace.AggregateLLMUsageFromResults(summary.Results)
	if !suiteTotal.AnyUsage() {
		// no LLM block
	} else if llmVerbose {
		fmt.Println()
		printLLMUsageExpanded("  ", "LLM suite (aggregated)", &suiteTotal, suiteByAPI, workspace.ComputeLLMUsageDerived(suiteTotal))
	}

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
