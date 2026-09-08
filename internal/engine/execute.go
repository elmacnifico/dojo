package engine

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/elmacnifico/dojo/internal/workspace"

	_ "github.com/lib/pq"
)

// MismatchError is returned when an actual payload does not match the expected
// one. It carries structured Expected/Actual data so callers (e.g. RunSuite)
// can populate [workspace.TestFailure] fields for rich reports, plus a
// pre-rendered line Diff for quick human triage.
type MismatchError struct {
	Reason   string
	Expected string
	Actual   string
	Diff     string
}

func (e *MismatchError) Error() string { return e.Reason }

func (e *Engine) executeTest(ctx context.Context, id string, test *workspace.Test, suite *workspace.Suite, suiteName string) (workspace.LLMUsage, map[string]workspace.LLMUsage, error) {
	var zero workspace.LLMUsage
	livePostgres := false
	var livePGURL string
	for _, api := range suite.APIs {
		if api.Protocol == "postgres" || strings.HasPrefix(api.URL, "postgres://") {
			if api.Mode == "live" {
				livePostgres = true
				livePGURL = api.URL
			}
		}
	}

	if livePostgres {
		seedDir := filepath.Join(e.Workspace.BaseDir, suiteName, id, "seed")
		e.perTestSeedMu.Lock()
		seedErr := e.runSeeds(livePGURL, seedDir)
		e.perTestSeedMu.Unlock()
		if seedErr != nil {
			ts := &TestSeedError{TestName: id, Err: seedErr}
			e.recordFirstSeedFailure(ts)
			return zero, nil, ts
		}
	}

	doc, err := workspace.ParsePlan(test.Plan)
	if err != nil {
		return zero, nil, fmt.Errorf("failed to parse plan: %w", err)
	}

	if len(doc.Lines) == 0 || strings.ToLower(doc.Lines[0].Action) != "perform" {
		return zero, nil, fmt.Errorf("plan must start with a Perform action")
	}

	phases := workspace.SplitPlanPhases(doc.Lines)
	if len(phases) == 0 {
		return zero, nil, fmt.Errorf("plan must start with a Perform action")
	}

	// Phase 1: entrypoint trigger + observer expectations.
	firstPhase := phases[0]

	active, ep, payload, expectStatus, err := e.prepareEntrypoint(ctx, id, test, suite, suiteName, firstPhase)
	if err != nil {
		return zero, nil, err
	}

	e.Registry.Register(id, active)
	defer e.Registry.Unregister(id)

	if err := e.triggerEntrypoint(ctx, suite, ep, payload, expectStatus); err != nil {
		return active.TotalUsage, filterLLMUsageByAPI(active.APIUsage), err
	}

	if err := e.awaitPhaseExpectations(ctx, active); err != nil {
		return active.TotalUsage, filterLLMUsageByAPI(active.APIUsage), err
	}

	// Subsequent phases: inline assertions (e.g. Perform -> postgres).
	if err := e.executeSubsequentPhases(ctx, active, id, suiteName, phases[1:], livePGURL); err != nil {
		return active.TotalUsage, filterLLMUsageByAPI(active.APIUsage), err
	}

	return active.TotalUsage, filterLLMUsageByAPI(active.APIUsage), nil
}

func (e *Engine) prepareEntrypoint(ctx context.Context, id string, test *workspace.Test, suite *workspace.Suite, suiteName string, phase workspace.PlanPhase) (*ActiveTest, workspace.EntrypointConfig, []byte, int, error) {
	testDir := filepath.Join(e.Workspace.BaseDir, suiteName, id)
	suiteDir := filepath.Join(e.Workspace.BaseDir, suiteName)

	ep, payload, expectStatus, err := workspace.ResolveHTTPPerform(phase.Perform, test, suite, testDir, suiteDir)
	if err != nil {
		return nil, ep, nil, 0, err
	}

	active := &ActiveTest{
		ID:           id,
		Test:         test,
		Suite:        suite,
		Ctx:          ctx,
		Expectations: make(map[string][]*Expectation),
		Variables:    make(map[string]any),
		APIUsage:     make(map[string]workspace.LLMUsage),
		done:         make(chan struct{}),
	}

	if len(payload) > 0 {
		var vars map[string]any
		if err := json.Unmarshal(payload, &vars); err == nil {
			active.Variables = vars
		}
	}

	if err := buildExpectations(active, phase.Expects, test, suite, fmt.Sprintf("test %s", id)); err != nil {
		return nil, ep, nil, 0, err
	}

	return active, ep, payload, expectStatus, nil
}

func (e *Engine) prepareStartupPlan(ctx context.Context, suite *workspace.Suite, suiteName string) (*ActiveTest, error) {
	doc, err := workspace.ParsePlan(suite.StartupPlan)
	if err != nil {
		return nil, fmt.Errorf("failed to parse startup plan: %w", err)
	}

	expectLines := 0
	for _, l := range doc.Lines {
		if strings.ToLower(l.Action) != "expect" {
			return nil, fmt.Errorf("startup plan can only contain Expect actions")
		}
		expectLines++
		var detail strings.Builder
		detail.WriteString(l.Action)
		detail.WriteString(" -> ")
		detail.WriteString(l.Target)
		for _, c := range l.Clauses {
			detail.WriteString(" ")
			detail.WriteString(c.Key)
			if c.Value != nil {
				detail.WriteString(": ")
				detail.WriteString(*c.Value)
			}
		}
		e.log.Debug("startup plan expect",
			"suite", suiteName,
			"line", expectLines,
			"detail", detail.String(),
		)
	}

	test := &workspace.Test{
		APIs:        make(map[string]workspace.APIConfig),
		Entrypoints: make(map[string]workspace.EntrypointConfig),
	}

	suiteDir := filepath.Join(e.Workspace.BaseDir, suiteName)
	if err := workspace.WireFixturesFromPlan(doc, test, suite, suiteDir, suiteDir); err != nil {
		return nil, fmt.Errorf("failed to wire fixtures for startup plan: %w", err)
	}

	active := &ActiveTest{
		ID:           "startup",
		Test:         test,
		Suite:        suite,
		Ctx:          ctx,
		Expectations: make(map[string][]*Expectation),
		APIUsage:     make(map[string]workspace.LLMUsage),
		done:         make(chan struct{}),
	}

	if err := buildExpectations(active, doc.Lines, test, suite, "startup plan"); err != nil {
		return nil, err
	}

	return active, nil
}

func (e *Engine) triggerEntrypoint(ctx context.Context, suite *workspace.Suite, ep workspace.EntrypointConfig, payload []byte, expectStatus int) error {
	switch ep.Type {
	case "http":
		url := ep.URL
		if url == "" {
			url = suite.Config.SutBaseURL
		}
		if url == "" {
			url = "http://127.0.0.1:8080"
		}

		req, err := http.NewRequestWithContext(ctx, ep.HTTPMethod(), url+ep.Path, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("failed to create entrypoint request: %w", err)
		}
		for k, v := range ep.Headers {
			req.Header.Set(k, v)
		}

		client := &http.Client{Timeout: suite.Config.Timeouts.Perform.Duration}
		if ep.FollowRedirects != nil && !*ep.FollowRedirects {
			client.CheckRedirect = func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("entrypoint trigger failed: %w", err)
		}
		defer resp.Body.Close()

		if expectStatus != 0 {
			if resp.StatusCode != expectStatus {
				return &MismatchError{
					Reason:   fmt.Sprintf("expected HTTP status %d, got %d", expectStatus, resp.StatusCode),
					Expected: strconv.Itoa(expectStatus),
					Actual:   strconv.Itoa(resp.StatusCode),
				}
			}
		} else if resp.StatusCode >= 400 {
			return fmt.Errorf("entrypoint returned HTTP %d", resp.StatusCode)
		}

		if ep.ExpectedResponse != nil && len(ep.ExpectedResponse.Payload) > 0 {
			respBody, err := io.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("failed to read entrypoint response: %w", err)
			}

			if !httpPayloadContains(respBody, ep.ExpectedResponse.Payload) {
				exp := truncate(string(ep.ExpectedResponse.Payload), 500)
				act := truncate(string(respBody), 500)
				return newMismatchError(
					fmt.Sprintf("entrypoint response mismatch\n  expected (substring): %s\n  actual:              %s", exp, act),
					exp, act)
			}
		}
	default:
		return fmt.Errorf("unsupported entrypoint type %q; only \"http\" is currently supported", ep.Type)
	}

	return nil
}

// evalDeadline grades the stored final response of a live multi-round flow
// whose call budget was never consumed (the model finalized before reaching
// MaxCalls). Runs synchronously inside the deadline goroutine so the phase
// cannot complete before the verdict is recorded.
func (e *Engine) evalDeadline(exp *Expectation, apiName string, active *ActiveTest, payload []byte) {
	var checkErr error
	if exp.RequiresEval {
		if err := e.Evaluate(active, payload); err != nil {
			checkErr = fmt.Errorf("AI Evaluation failed: %w", err)
		}
	}
	active.MarkFulfilled(apiName, exp.Index, checkErr)
}

func (e *Engine) awaitPhaseExpectations(ctx context.Context, active *ActiveTest) error {
	// Launch per-expectation timeout goroutines.
	for api, exps := range active.Expectations {
		for _, exp := range exps {
			go func(apiName string, exp *Expectation) {
				timer := time.NewTimer(exp.Deadline)
				defer timer.Stop()
				select {
				case <-timer.C:
					// Live multi-round flows that matched at least one
					// request but never consumed the full MaxCalls budget
					// ended normally (fewer rounds than budgeted): grade
					// the stored final response instead of failing. The
					// evalDeadline path fulfills (or fails) the expectation
					// with the real verdict.
					if exp.RequiresEval {
						if lastPayload, ok := active.FinalLiveResponse(apiName, exp.Index); ok {
							e.evalDeadline(exp, apiName, active, lastPayload)
							return
						}
					}
					active.MarkFulfilled(apiName, exp.Index,
						fmt.Errorf("timed out after %s waiting for expected request", exp.Deadline))
				case <-active.done:
				case <-ctx.Done():
				}
			}(api, exp)
		}
	}

	// Wait for phase expectations.
	select {
	case <-active.done:
	case <-e.sutDeadCh:
		return fmt.Errorf("SUT process crashed while test was running: %v", e.SUTError())
	case <-ctx.Done():
		var unfulfilled []string
		for api, exps := range active.Expectations {
			for i, exp := range exps {
				if !exp.Fulfilled {
					if len(exps) > 1 {
						unfulfilled = append(unfulfilled, fmt.Sprintf("%s[%d]", api, i))
					} else {
						unfulfilled = append(unfulfilled, api)
					}
				}
			}
		}
		return fmt.Errorf("test timed out waiting for expectations: %v", unfulfilled)
	}

	for api, exps := range active.Expectations {
		for i, exp := range exps {
			if exp.Error != nil {
				if len(exps) > 1 {
					return fmt.Errorf("expectation for %s[%d] failed: %w", api, i, exp.Error)
				}
				return fmt.Errorf("expectation for %s failed: %w", api, exp.Error)
			}
		}
	}

	return nil
}

func (e *Engine) executeSubsequentPhases(ctx context.Context, active *ActiveTest, id, suiteName string, phases []workspace.PlanPhase, livePGURL string) error {
	testDir := filepath.Join(e.Workspace.BaseDir, suiteName, id)
	suiteDir := filepath.Join(e.Workspace.BaseDir, suiteName)
	for _, ph := range phases {
		if workspace.IsWaitPerformTarget(ph.Perform) {
			if err := e.executeWaitPerform(ctx, ph.Perform); err != nil {
				return err
			}
			continue
		}
		if err := e.executePostgresPerform(ctx, active, ph.Perform, testDir, suiteDir, livePGURL); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) executeWaitPerform(ctx context.Context, line workspace.ParsedLine) error {
	d, err := workspace.ParseWaitPerformDuration(line)
	if err != nil {
		return err
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// executePostgresPerform runs a SQL query against the live Postgres and asserts
// on the result. Three modes based on the Expect clause:
//   - No Expect:       query must not error (OK)
//   - Expect: "N":     query must return exactly N rows
//   - Expect: file.json: result rows compared via JSONSubsetMatch
func (e *Engine) executePostgresPerform(ctx context.Context, active *ActiveTest, line workspace.ParsedLine, testDir, suiteDir, pgURL string) error {
	var queryFile, expectValue string
	positionalCount := 0

	for _, c := range line.Clauses {
		if c.Value == nil {
			// Positional argument
			if positionalCount == 0 {
				queryFile = c.Key
			} else if positionalCount == 1 {
				expectValue = c.Key
			}
			positionalCount++
			continue
		}
		switch strings.ToLower(c.Key) {
		case "query":
			queryFile = *c.Value
		case "expect":
			expectValue = *c.Value
		}
	}

	if queryFile == "" {
		return fmt.Errorf("Perform -> postgres requires a Query clause")
	}

	querySQL, err := workspace.ReadPlanFixture(testDir, suiteDir, queryFile)
	if err != nil {
		return fmt.Errorf("failed to read query fixture %s: %w", queryFile, err)
	}

	queryStr := string(querySQL)
	if active != nil && len(active.Variables) > 0 {
		tmpl, err := template.New("sql").Parse(queryStr)
		if err == nil {
			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, active.Variables); err == nil {
				queryStr = buf.String()
			}
		}
	}

	db, err := sql.Open("postgres", pgURL)
	if err != nil {
		return fmt.Errorf("postgres connect failed: %w", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, queryStr)
	if err != nil {
		return fmt.Errorf("postgres query failed: %w", err)
	}
	defer rows.Close()

	if expectValue == "" {
		return nil
	}

	if filepath.Ext(expectValue) != "" {
		actual, err := rowsToJSON(rows)
		if err != nil {
			return fmt.Errorf("reading postgres result rows: %w", err)
		}
		expected, err := workspace.ReadPlanFixture(testDir, suiteDir, expectValue)
		if err != nil {
			return fmt.Errorf("failed to read expect fixture %s: %w", expectValue, err)
		}
		if !workspace.JSONSubsetMatch(actual, expected) {
			exp := truncate(string(expected), 500)
			act := truncate(string(actual), 500)
			return newMismatchError(
				fmt.Sprintf("postgres result mismatch\n  expected: %s\n  actual:   %s", exp, act),
				exp, act)
		}
		return nil
	}

	expectedCount, err := strconv.Atoi(expectValue)
	if err != nil {
		return fmt.Errorf("invalid Expect value %q: must be a number or a .json file path", expectValue)
	}
	actualCount := 0
	for rows.Next() {
		actualCount++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating result rows: %w", err)
	}
	if actualCount != expectedCount {
		return &MismatchError{
			Reason:   fmt.Sprintf("expected %d rows, got %d", expectedCount, actualCount),
			Expected: strconv.Itoa(expectedCount),
			Actual:   strconv.Itoa(actualCount),
		}
	}
	return nil
}

// rowsToJSON serializes SQL result rows as a JSON array of string-valued
// objects. Scan errors are surfaced through the returned error so a corrupt
// result set fails the test instead of silently producing partial JSON.
func rowsToJSON(rows *sql.Rows) ([]byte, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("reading result columns: %w", err)
	}
	var results []map[string]string
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("scanning result row: %w", err)
		}
		row := make(map[string]string, len(cols))
		for i, col := range cols {
			if vals[i].Valid {
				row[col] = vals[i].String
			}
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating result rows: %w", err)
	}
	if results == nil {
		results = []map[string]string{}
	}
	b, err := json.Marshal(results)
	if err != nil {
		return nil, fmt.Errorf("marshaling result rows: %w", err)
	}
	return b, nil
}

func (e *Engine) checkSeedRequiresLiveDB(seedDir string, hasLiveDB bool) error {
	entries, err := os.ReadDir(seedDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading seed directory %s: %w", seedDir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			if !hasLiveDB {
				return fmt.Errorf("seed script %s found in %s but no live Postgres API is configured", entry.Name(), seedDir)
			}
			return nil
		}
	}
	return nil
}

func (e *Engine) runSeeds(dbURL string, seedDir string) error {
	entries, err := os.ReadDir(seedDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading seed directory %s: %w", seedDir, err)
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return fmt.Errorf("failed to connect to postgres for seeding: %w", err)
	}
	defer db.Close()

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			scriptPath := filepath.Join(seedDir, entry.Name())
			b, err := os.ReadFile(scriptPath)
			if err != nil {
				return fmt.Errorf("failed to read seed script %s: %w", scriptPath, err)
			}

			if _, err := db.Exec(string(b)); err != nil {
				return fmt.Errorf("failed to execute seed script %s: %w", scriptPath, err)
			}
			e.log.Debug("executed seed script", "path", scriptPath)
		}
	}

	return nil
}

// Evaluate delegates complex payload assertion to the configured GenAI model.
func (e *Engine) Evaluate(activeTest *ActiveTest, payload []byte) error {
	evalRule := activeTest.Test.Eval
	if evalRule == "" {
		return fmt.Errorf("no eval.md rule found for test %s but Evaluate Response was requested", activeTest.ID)
	}

	cfg := activeTest.Suite.Config.Evaluator
	if cfg == nil {
		return fmt.Errorf("evaluator config missing in dojo.yaml")
	}

	// Build the evaluator once per engine; the parsed prompt template and
	// HTTP client are reused across every Evaluate Response clause. The
	// ai_evaluator timeout bounds each provider attempt — retried attempts
	// get a fresh budget, and the per-test context below still caps the
	// whole evaluation.
	e.evaluatorMu.Lock()
	if e.evaluator == nil {
		attemptTimeout := activeTest.Suite.Config.Timeouts.AIEvaluator.Duration
		if attemptTimeout <= 0 {
			attemptTimeout = workspace.DefaultAIEvaluator
		}
		ev, err := NewAIEvaluator(cfg, evaluatorPromptTemplate, attemptTimeout)
		if err != nil {
			e.evaluatorMu.Unlock()
			return fmt.Errorf("creating evaluator: %w", err)
		}
		e.evaluator = ev
	}
	evaluator := e.evaluator
	e.evaluatorMu.Unlock()

	parent := activeTest.Ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, activeTest.Suite.Config.Timeouts.AIEvaluator.Duration)
	defer cancel()

	result, err := evaluator.Evaluate(ctx, payload, evalRule)
	if err != nil {
		return fmt.Errorf("evaluation error: %w", err)
	}

	if !result.Pass {
		return fmt.Errorf("AI Evaluation failed: %s", result.Reason)
	}

	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
