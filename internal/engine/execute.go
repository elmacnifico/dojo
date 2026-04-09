package engine

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"dojo/internal/workspace"

	_ "github.com/lib/pq"
)

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

	performLine := doc.Lines[0]
	epName := strings.TrimPrefix(performLine.Target, "entrypoints/")
	ep, ok := suite.Entrypoints[epName]
	if !ok {
		return fmt.Errorf("entrypoint '%s' not found", epName)
	}

	var payload []byte
	for _, clause := range performLine.Clauses {
		if strings.ToLower(clause.Key) == "payload" && clause.Value != nil {
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
	}

	active := &ActiveTest{
		ID:           id,
		Test:         test,
		Suite:        suite,
		Expectations: make(map[string]*Expectation),
		done:         make(chan struct{}),
	}

	for _, l := range doc.Lines {
		if strings.ToLower(l.Action) == "expect" {
			apiName := l.Target
			exp := &Expectation{
				Target:    apiName,
				Fulfilled: false,
			}

			for _, clause := range l.Clauses {
				if strings.ToLower(clause.Key) == "evaluate response" {
					exp.RequiresEval = true
				}
			}
			active.Expectations[apiName] = exp
		}
	}

	if len(active.Expectations) == 0 {
		close(active.done)
	}

	e.Registry.Register(id, active)
	defer e.Registry.Unregister(id)

	if ep.Type == "http" {
		url := ep.URL
		if url == "" {
			url = "http://127.0.0.1:8080"
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+ep.Path, bytes.NewReader(payload))
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

		if resp.StatusCode >= 400 {
			return fmt.Errorf("entrypoint returned HTTP %d", resp.StatusCode)
		}

		if ep.ExpectedResponse != nil && len(ep.ExpectedResponse.Payload) > 0 {
			respBody, err := io.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("failed to read entrypoint response: %w", err)
			}

			if !httpPayloadContains(respBody, ep.ExpectedResponse.Payload) {
				return fmt.Errorf("entrypoint response mismatch\n  expected (substring): %s\n  actual:              %s",
					truncate(string(ep.ExpectedResponse.Payload), 200),
					truncate(string(respBody), 200))
			}
		}
	}

	select {
	case <-active.done:
	case <-ctx.Done():
		var unfulfilled []string
		for api, exp := range active.Expectations {
			if !exp.Fulfilled {
				unfulfilled = append(unfulfilled, api)
			}
		}
		return fmt.Errorf("test timed out waiting for expectations: %v", unfulfilled)
	}

	for api, exp := range active.Expectations {
		if exp.Error != nil {
			return fmt.Errorf("expectation for %s failed: %w", api, exp.Error)
		}
	}

	return nil
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
			e.log.Info("executed seed script", "path", scriptPath)
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

	ctx, cancel := context.WithTimeout(context.Background(), activeTest.Suite.Config.Timeouts.AIEvaluator.Duration)
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
