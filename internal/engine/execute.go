package engine

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"text/template"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dojo/internal/workspace"

	_ "github.com/lib/pq"
)

// MismatchError is returned when an actual payload does not match the expected
// one. It carries structured Expected/Actual data so callers (e.g. RunSuite)
// can populate [workspace.TestFailure] fields for rich reports.
type MismatchError struct {
	Reason   string
	Expected string
	Actual   string
}

func (e *MismatchError) Error() string { return e.Reason }

// maxCallsFromExpectLine parses an optional MaxCalls clause from an Expect line.
// When present, the value must be a positive integer (matching WireFixturesFromPlan).
func maxCallsFromExpectLine(l workspace.ParsedLine) (max int, found bool, err error) {
	for _, clause := range l.Clauses {
		if clause.Value == nil {
			continue
		}
		if strings.ToLower(clause.Key) != "maxcalls" {
			continue
		}
		v := strings.TrimSpace(*clause.Value)
		m, convErr := strconv.Atoi(v)
		if convErr != nil {
			return 0, true, fmt.Errorf("MaxCalls must be an integer, got %q", *clause.Value)
		}
		if m < 1 {
			return 0, true, fmt.Errorf("MaxCalls must be at least 1, got %d", m)
		}
		return m, true, nil
	}
	return 0, false, nil
}

func (e *Engine) executeTest(ctx context.Context, id string, test *workspace.Test, suite *workspace.Suite, suiteName string) (workspace.LLMUsage, error) {
	var usage workspace.LLMUsage
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
		if err := e.runSeeds(livePGURL, filepath.Join(e.Workspace.BaseDir, suiteName, id, "seed")); err != nil {
			return usage, fmt.Errorf("test seeding failed: %w", err)
		}
	}

	doc, err := workspace.ParsePlan(test.Plan)
	if err != nil {
		return usage, fmt.Errorf("failed to parse plan: %w", err)
	}

	if len(doc.Lines) == 0 || strings.ToLower(doc.Lines[0].Action) != "perform" {
		return usage, fmt.Errorf("plan must start with a Perform action")
	}

	phases := workspace.SplitPlanPhases(doc.Lines)
	if len(phases) == 0 {
		return usage, fmt.Errorf("plan must start with a Perform action")
	}

	// Phase 1: entrypoint trigger + observer expectations.
	firstPhase := phases[0]

	active, ep, payload, expectStatus, err := e.prepareEntrypoint(ctx, id, test, suite, suiteName, firstPhase)
	if err != nil {
		return usage, err
	}

	e.Registry.Register(id, active)
	defer e.Registry.Unregister(id)

	if err := e.triggerEntrypoint(ctx, suite, ep, payload, expectStatus); err != nil {
		return active.TotalUsage, err
	}

	if err := e.awaitPhaseExpectations(ctx, active); err != nil {
		return active.TotalUsage, err
	}

	// Subsequent phases: inline assertions (e.g. Perform -> postgres).
	if err := e.executeSubsequentPhases(ctx, active, id, suiteName, phases[1:], livePGURL); err != nil {
		return active.TotalUsage, err
	}

	return active.TotalUsage, nil
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

	expIdx := make(map[string]int)
	for _, l := range phase.Expects {
		apiName := l.Target
		if idx := strings.IndexByte(apiName, '/'); idx >= 0 {
			apiName = apiName[:idx]
		}
		idx := expIdx[apiName]
		exp := &Expectation{
			Target: apiName,
			Index:  idx,
		}
		if d := test.APIs[apiName].TimeoutDuration(); d > 0 {
			exp.Deadline = d
		} else {
			exp.Deadline = suite.Config.Timeouts.Expect.Duration
		}
		for _, clause := range l.Clauses {
			if strings.ToLower(clause.Key) == "evaluate response" {
				exp.RequiresEval = true
			}
		}
		mc, mcHas, mcErr := maxCallsFromExpectLine(l)
		if mcErr != nil {
			return nil, ep, nil, 0, fmt.Errorf("test %s expect %s: %w", id, l.Target, mcErr)
		}
		if mcHas {
			exp.MaxCalls = mc
		}
		active.Expectations[apiName] = append(active.Expectations[apiName], exp)
		expIdx[apiName] = idx + 1
	}

	if len(active.Expectations) == 0 {
		close(active.done)
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

	expIdx := make(map[string]int)
	for _, l := range doc.Lines {
		apiName := l.Target
		if idx := strings.IndexByte(apiName, '/'); idx >= 0 {
			apiName = apiName[:idx]
		}
		idx := expIdx[apiName]
		exp := &Expectation{
			Target: apiName,
			Index:  idx,
		}
		if d := test.APIs[apiName].TimeoutDuration(); d > 0 {
			exp.Deadline = d
		} else {
			exp.Deadline = suite.Config.Timeouts.Expect.Duration
		}
		for _, clause := range l.Clauses {
			if strings.ToLower(clause.Key) == "evaluate response" {
				exp.RequiresEval = true
			}
		}
		mc, mcHas, mcErr := maxCallsFromExpectLine(l)
		if mcErr != nil {
			return nil, fmt.Errorf("startup plan expect %s: %w", l.Target, mcErr)
		}
		if mcHas {
			exp.MaxCalls = mc
		}
		active.Expectations[apiName] = append(active.Expectations[apiName], exp)
		expIdx[apiName] = idx + 1
	}

	if len(active.Expectations) == 0 {
		close(active.done)
	}

	return active, nil
}

func (e *Engine) triggerEntrypoint(ctx context.Context, suite *workspace.Suite, ep workspace.EntrypointConfig, payload []byte, expectStatus int) error {
	switch ep.Type {
	case "http":
		url := ep.URL
		if url == "" {
			url = suite.Config.SutBaseURL
		if url == "" {
			url = "http://127.0.0.1:8080"
		}
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
				return &MismatchError{
					Reason:   fmt.Sprintf("entrypoint response mismatch\n  expected (substring): %s\n  actual:              %s", exp, act),
					Expected: exp,
					Actual:   act,
				}
			}
		}
	default:
		return fmt.Errorf("unsupported entrypoint type %q; only \"http\" is currently supported", ep.Type)
	}

	return nil
}

func (e *Engine) awaitPhaseExpectations(ctx context.Context, active *ActiveTest) error {
	// Launch per-expectation timeout goroutines.
	for api, exps := range active.Expectations {
		for _, exp := range exps {
			go func(apiName string, e *Expectation) {
				timer := time.NewTimer(e.Deadline)
				defer timer.Stop()
				select {
				case <-timer.C:
					active.MarkFulfilled(apiName, e.Index,
						fmt.Errorf("timed out after %s waiting for expected request", e.Deadline))
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
		if err := e.executePostgresPerform(ctx, active, ph.Perform, testDir, suiteDir, livePGURL); err != nil {
			return err
		}
	}
	return nil
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
		actual := rowsToJSON(rows)
		expected, err := workspace.ReadPlanFixture(testDir, suiteDir, expectValue)
		if err != nil {
			return fmt.Errorf("failed to read expect fixture %s: %w", expectValue, err)
		}
		if !workspace.JSONSubsetMatch(actual, expected) {
			exp := truncate(string(expected), 500)
			act := truncate(string(actual), 500)
			return &MismatchError{
				Reason:   fmt.Sprintf("postgres result mismatch\n  expected: %s\n  actual:   %s", exp, act),
				Expected: exp,
				Actual:   act,
			}
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
	if actualCount != expectedCount {
		return &MismatchError{
			Reason:   fmt.Sprintf("expected %d rows, got %d", expectedCount, actualCount),
			Expected: strconv.Itoa(expectedCount),
			Actual:   strconv.Itoa(actualCount),
		}
	}
	return nil
}

// rowsToJSON serializes SQL result rows as a JSON array of string-valued objects.
func rowsToJSON(rows *sql.Rows) []byte {
	cols, _ := rows.Columns()
	var results []map[string]string
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		rows.Scan(ptrs...)
		row := make(map[string]string, len(cols))
		for i, col := range cols {
			if vals[i].Valid {
				row[col] = vals[i].String
			}
		}
		results = append(results, row)
	}
	if results == nil {
		results = []map[string]string{}
	}
	b, _ := json.Marshal(results)
	return b
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

	evaluator, err := NewAIEvaluator(cfg, "You are a strict test evaluator. Decide whether the ACTUAL PAYLOAD satisfies every rule in EXPECTED RULE.\n\nEXPECTED RULE:\n{{.ExpectedRule}}\n\nACTUAL PAYLOAD:\n{{.ActualPayload}}\n\nRespond with ONLY a JSON object in this exact format (no markdown, no extra text):\n{\"pass\": true, \"reason\": \"short explanation\"}\nSet \"pass\" to true if ALL rules are satisfied, false otherwise. Always include a \"reason\".")
	if err != nil {
		return fmt.Errorf("creating evaluator: %w", err)
	}

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
