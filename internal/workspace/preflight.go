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
// (if configured) and Perform resolution for every test plan phase.
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
	for testName, test := range suite.Tests {
		testDir := filepath.Join(suiteDir, testName)
		if err := ValidateTestPlanPerformPhases(suite, test, suiteDir, testDir); err != nil {
			return fmt.Errorf("test %s: %w", testName, err)
		}
	}
	return nil
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
		if err := ValidatePostgresPerformLine(ph.Perform, testDir, suiteDir); err != nil {
			return err
		}
	}
	return nil
}
