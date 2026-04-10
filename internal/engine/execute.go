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

// planPhase groups a Perform line with the Expect lines that follow it.
type planPhase struct {
	perform workspace.ParsedLine
	expects []workspace.ParsedLine
}

// splitPhases divides plan lines into phases, each starting with a Perform.
func splitPhases(lines []workspace.ParsedLine) []planPhase {
	var phases []planPhase
	cur := -1
	for _, l := range lines {
		switch strings.ToLower(l.Action) {
		case "perform":
			phases = append(phases, planPhase{perform: l})
			cur++
		case "expect":
			if cur >= 0 {
				phases[cur].expects = append(phases[cur].expects, l)
			}
		}
	}
	return phases
}

func (e *Engine) executeTest(ctx context.Context, id string, test *workspace.Test, suite *workspace.Suite, suiteName string) error {
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
			return fmt.Errorf("test seeding failed: %w", err)
		}
	}

	doc, err := workspace.ParsePlan(test.Plan)
	if err != nil {
		return fmt.Errorf("failed to parse plan: %w", err)
	}

	if len(doc.Lines) == 0 || strings.ToLower(doc.Lines[0].Action) != "perform" {
		return fmt.Errorf("plan must start with a Perform action")
	}

	phases := splitPhases(doc.Lines)
	if len(phases) == 0 {
		return fmt.Errorf("plan must start with a Perform action")
	}

	// Phase 1: entrypoint trigger + observer expectations.
	firstPhase := phases[0]
	performLine := firstPhase.perform
	epName := strings.TrimPrefix(performLine.Target, "entrypoints/")
	ep, ok := suite.Entrypoints[epName]
	if !ok {
		return fmt.Errorf("entrypoint '%s' not found", epName)
	}

	var payload []byte
	var expectStatus int
	for _, clause := range performLine.Clauses {
		switch strings.ToLower(clause.Key) {
		case "payload":
			if clause.Value != nil {
				if filepath.Ext(*clause.Value) != "" {
					payloadPath := filepath.Join(e.Workspace.BaseDir, suiteName, id, *clause.Value)
					b, err := os.ReadFile(payloadPath)
					if err != nil {
						fallbackPath := filepath.Join(e.Workspace.BaseDir, suiteName, *clause.Value)
						b, err = os.ReadFile(fallbackPath)
						if err != nil {
							return fmt.Errorf("failed to read payload %s: %w", payloadPath, err)
						}
					}
					payload = b
				} else {
					payload = []byte(*clause.Value)
				}
			}
		case "expectstatus":
			if clause.Value != nil {
				n, err := strconv.Atoi(*clause.Value)
				if err != nil {
					return fmt.Errorf("invalid ExpectStatus value %q: must be an integer", *clause.Value)
				}
				expectStatus = n
			}
		}
	}

	active := &ActiveTest{
		ID:           id,
		Test:         test,
		Suite:        suite,
		Ctx:          ctx,
		Expectations: make(map[string][]*Expectation),
		done:         make(chan struct{}),
	}

	expIdx := make(map[string]int)
	for _, l := range firstPhase.expects {
		apiName := l.Target
		idx := expIdx[apiName]
		exp := &Expectation{
			Target: apiName,
			Index:  idx,
		}
		for _, clause := range l.Clauses {
			if strings.ToLower(clause.Key) == "evaluate response" {
				exp.RequiresEval = true
			}
		}
		active.Expectations[apiName] = append(active.Expectations[apiName], exp)
		expIdx[apiName] = idx + 1
	}

	if len(active.Expectations) == 0 {
		close(active.done)
	}

	e.Registry.Register(id, active)
	defer e.Registry.Unregister(id)

	switch ep.Type {
	case "http":
		url := ep.URL
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

		client := &http.Client{Timeout: suite.Config.Timeouts.HTTPClient.Duration}
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
		return fmt.Errorf("unsupported entrypoint type %q for %q; only \"http\" is currently supported", ep.Type, epName)
	}

	// Wait for phase 1 expectations.
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

	// Subsequent phases: inline assertions (e.g. Perform -> postgres).
	testDir := filepath.Join(e.Workspace.BaseDir, suiteName, id)
	suiteDir := filepath.Join(e.Workspace.BaseDir, suiteName)
	for _, ph := range phases[1:] {
		if err := e.executePostgresPerform(ctx, ph.perform, testDir, suiteDir, livePGURL); err != nil {
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
func (e *Engine) executePostgresPerform(ctx context.Context, line workspace.ParsedLine, testDir, suiteDir, pgURL string) error {
	var queryFile, expectValue string
	for _, c := range line.Clauses {
		if c.Value == nil {
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

	querySQL, err := readFixture(testDir, suiteDir, queryFile)
	if err != nil {
		return fmt.Errorf("failed to read query fixture %s: %w", queryFile, err)
	}

	db, err := sql.Open("postgres", pgURL)
	if err != nil {
		return fmt.Errorf("postgres connect failed: %w", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, string(querySQL))
	if err != nil {
		return fmt.Errorf("postgres query failed: %w", err)
	}
	defer rows.Close()

	if expectValue == "" {
		return nil
	}

	if filepath.Ext(expectValue) != "" {
		actual := rowsToJSON(rows)
		expected, err := readFixture(testDir, suiteDir, expectValue)
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

// readFixture reads a file, trying testDir first then falling back to suiteDir.
func readFixture(testDir, suiteDir, filename string) ([]byte, error) {
	p := filepath.Join(testDir, filename)
	b, err := os.ReadFile(p)
	if err != nil {
		p = filepath.Join(suiteDir, filename)
		b, err = os.ReadFile(p)
		if err != nil {
			return nil, err
		}
	}
	return b, nil
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
		return fmt.Errorf("evaluator config missing in dojo.config")
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
