package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CheckEvaluatorAPIKey returns an error if the suite configures an AI evaluator
// but the API key environment variable is unset. Call after loading suite .env
// files into the process environment.
func CheckEvaluatorAPIKey(suite *Suite) error {
	if suite == nil || suite.Config.Evaluator == nil {
		return nil
	}
	env := suite.Config.Evaluator.APIKeyEnv
	if env == "" {
		return nil
	}
	if os.Getenv(env) == "" {
		return fmt.Errorf("evaluator is configured (api_key_env=%q) but that environment variable is not set; set it or add it to .env / .env.local in the suite directory", env)
	}
	return nil
}

// PreflightLoadedSuite runs static checks on a loaded suite: evaluator API key
// (if configured), Perform resolution for every test plan phase, and seed/check
// SQL validations when live Postgres is configured.
func PreflightLoadedSuite(ws *Workspace, suiteName string) error {
	if ws == nil {
		return fmt.Errorf("workspace is nil")
	}
	suite, ok := ws.Suites[suiteName]
	if !ok {
		return fmt.Errorf("suite %q not found", suiteName)
	}
	if err := CheckEvaluatorAPIKey(suite); err != nil {
		return err
	}
	suiteDir := filepath.Join(ws.BaseDir, suiteName)
	if err := ValidateStartupPlanFixtures(ws, suite, suiteDir); err != nil {
		return err
	}
	for testName, test := range suite.Tests {
		testDir := filepath.Join(suiteDir, testName)
		if err := ValidateTestPlanStatic(suite, test, suiteDir, testDir); err != nil {
			return fmt.Errorf("test %s: %w", testName, err)
		}
	}
	if hasLivePostgres(suite) {
		if err := ValidateUniqueSeedKeys(suiteDir, suite.Tests); err != nil {
			return fmt.Errorf("seed validation: %w", err)
		}
		if err := ValidateCheckSQLScoping(suiteDir, suite.Tests); err != nil {
			return fmt.Errorf("check SQL validation: %w", err)
		}
	}
	return nil
}

// hasLivePostgres reports whether the suite has at least one live Postgres API.
func hasLivePostgres(suite *Suite) bool {
	for _, api := range suite.APIs {
		if (api.Protocol == "postgres" || strings.HasPrefix(api.URL, "postgres://")) && api.Mode == "live" {
			return true
		}
	}
	return false
}

// ValidateStartupPlanFixtures parses startup.plan and wires Expect fixtures the
// same way the engine does during StartProxies.
func ValidateStartupPlanFixtures(ws *Workspace, suite *Suite, suiteDir string) error {
	if suite == nil || strings.TrimSpace(suite.StartupPlan) == "" {
		return nil
	}
	doc, err := ParsePlan(suite.StartupPlan)
	if err != nil {
		return fmt.Errorf("startup.plan parse: %w", err)
	}
	for _, line := range doc.Lines {
		if strings.ToLower(line.Action) != "expect" {
			return fmt.Errorf("startup.plan can only contain Expect actions, got %q", line.Action)
		}
	}
	scratch := &Test{
		APIs:        make(map[string]APIConfig),
		Entrypoints: make(map[string]EntrypointConfig),
	}
	if err := WireFixturesFromPlan(doc, scratch, suite, suiteDir, suiteDir); err != nil {
		return fmt.Errorf("startup.plan: %w", err)
	}
	if err := validateExpectLinesForEval(doc, effectiveSuiteEval(ws, suite)); err != nil {
		return fmt.Errorf("startup.plan: %w", err)
	}
	return nil
}

func effectiveSuiteEval(ws *Workspace, suite *Suite) string {
	s := strings.TrimSpace(suite.Eval)
	if s != "" {
		return s
	}
	if ws != nil {
		return strings.TrimSpace(ws.GlobalEval)
	}
	return ""
}

// validateExpectLinesForEval ensures every Evaluate Response clause has a
// non-empty eval rule string for this context.
func validateExpectLinesForEval(doc *ParsedDocument, evalRule string) error {
	for _, line := range doc.Lines {
		if strings.ToLower(line.Action) != "expect" {
			continue
		}
		requiresEval := false
		for _, c := range line.Clauses {
			if strings.EqualFold(c.Key, "evaluate response") {
				requiresEval = true
				break
			}
		}
		if !requiresEval {
			continue
		}
		if strings.TrimSpace(evalRule) == "" {
			return fmt.Errorf("Expect -> %s uses Evaluate Response but no eval rules are configured (add eval.md at workspace, suite, or test level)", line.Target)
		}
	}
	return nil
}

// ValidateTestPlanStatic runs [ValidateTestPlanPerformPhases] plus Expect-line
// checks (known API, MaxCalls, Evaluate Response vs eval.md).
func ValidateTestPlanStatic(suite *Suite, test *Test, suiteDir, testDir string) error {
	if err := ValidateTestPlanPerformPhases(suite, test, suiteDir, testDir); err != nil {
		return err
	}
	doc, err := ParsePlan(test.Plan)
	if err != nil {
		return fmt.Errorf("parse plan: %w", err)
	}
	phases := SplitPlanPhases(doc.Lines)
	for _, ph := range phases {
		for _, line := range ph.Expects {
			if err := validateSingleExpectLine(line, test, suite); err != nil {
				return err
			}
		}
	}
	if err := validateExpectLinesForEval(doc, strings.TrimSpace(test.Eval)); err != nil {
		return err
	}
	return nil
}

func validateSingleExpectLine(line ParsedLine, test *Test, suite *Suite) error {
	apiName := line.Target
	if idx := strings.IndexByte(apiName, '/'); idx >= 0 {
		apiName = apiName[:idx]
	}
	if !expectAPIConfigured(apiName, test, suite) {
		return fmt.Errorf("Expect -> %s: API %q is not defined", line.Target, apiName)
	}
	if _, _, err := ParseMaxCallsFromExpectLine(line); err != nil {
		return fmt.Errorf("Expect -> %s: %w", line.Target, err)
	}
	return nil
}

func expectAPIConfigured(apiName string, test *Test, suite *Suite) bool {
	if _, ok := test.APIs[apiName]; ok {
		return true
	}
	_, ok := suite.APIs[apiName]
	return ok
}

// ValidateTestPlanPerformPhases ensures each Perform phase can resolve entrypoints,
// payloads, and postgres fixtures the same way the engine will at runtime.
func ValidateTestPlanPerformPhases(suite *Suite, test *Test, suiteDir, testDir string) error {
	doc, err := ParsePlan(test.Plan)
	if err != nil {
		return fmt.Errorf("parse plan: %w", err)
	}
	if len(doc.Lines) == 0 || strings.ToLower(doc.Lines[0].Action) != "perform" {
		return fmt.Errorf("plan must start with a Perform action")
	}
	phases := SplitPlanPhases(doc.Lines)
	for i, ph := range phases {
		if i == 0 {
			if _, _, _, err := ResolveHTTPPerform(ph.Perform, test, suite, testDir, suiteDir); err != nil {
				return err
			}
			continue
		}
		if IsWaitPerformTarget(ph.Perform) {
			if len(ph.Expects) > 0 {
				return fmt.Errorf("Perform -> wait cannot be followed by Expect lines in the same phase")
			}
			if err := ValidateWaitPerformLine(ph.Perform); err != nil {
				return err
			}
			continue
		}
		if err := ValidatePostgresPerformLine(ph.Perform, testDir, suiteDir); err != nil {
			return err
		}
	}
	return nil
}
