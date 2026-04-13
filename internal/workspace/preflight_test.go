package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dojo/internal/testutil"
)

func TestCheckEvaluatorAPIKey_Unset(t *testing.T) {
	t.Setenv("DOJO_PREFLIGHT_KEY_XYZ", "")
	suite := &Suite{
		Config: DojoConfig{
			Evaluator: &EvaluatorConfig{
				Provider:  "gemini",
				Model:     "gemini-1.5-flash",
				APIKeyEnv: "DOJO_PREFLIGHT_KEY_XYZ",
			},
		},
	}
	if err := CheckEvaluatorAPIKey(suite); err == nil {
		t.Fatal("expected error when API key env is unset")
	}
}

func TestCheckEvaluatorAPIKey_Set(t *testing.T) {
	t.Setenv("DOJO_PREFLIGHT_KEY_OK", "secret")
	suite := &Suite{
		Config: DojoConfig{
			Evaluator: &EvaluatorConfig{
				Provider:  "gemini",
				Model:     "gemini-1.5-flash",
				APIKeyEnv: "DOJO_PREFLIGHT_KEY_OK",
			},
		},
	}
	if err := CheckEvaluatorAPIKey(suite); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckEvaluatorAPIKey_NoEvaluator(t *testing.T) {
	t.Parallel()
	if err := CheckEvaluatorAPIKey(&Suite{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckEvaluatorAPIKey_NilSuite(t *testing.T) {
	t.Parallel()
	if err := CheckEvaluatorAPIKey(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckEvaluatorAPIKey_EmptyAPIKeyEnvName(t *testing.T) {
	t.Parallel()
	suite := &Suite{
		Config: DojoConfig{
			Evaluator: &EvaluatorConfig{
				Provider:  "gemini",
				Model:     "m",
				APIKeyEnv: "",
			},
		},
	}
	if err := CheckEvaluatorAPIKey(suite); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTestPlanPerformPhases_UnknownEntrypoint(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	suite := &Suite{
		Entrypoints: map[string]EntrypointConfig{
			"webhook": {Type: "http", Path: "/hook"},
		},
	}
	test := &Test{Plan: "Perform -> entrypoints/nope -> Payload: x.json"}
	if err := ValidateTestPlanPerformPhases(suite, test, tmp, tmp); err == nil {
		t.Fatal("expected error for missing entrypoint")
	}
}

func TestValidateTestPlanPerformPhases_PostgresPhase(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "q.sql"), []byte("SELECT 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	suite := &Suite{Entrypoints: map[string]EntrypointConfig{}}
	test := &Test{
		Plan: `Perform -> POST /x
Perform -> postgres -> q.sql -> "1"
`,
	}
	if err := ValidateTestPlanPerformPhases(suite, test, tmp, tmp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTestPlanPerformPhases_EmptyPlan(t *testing.T) {
	t.Parallel()
	suite := &Suite{}
	test := &Test{Plan: ""}
	err := ValidateTestPlanPerformPhases(suite, test, t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateTestPlanPerformPhases_FirstLineNotPerform(t *testing.T) {
	t.Parallel()
	suite := &Suite{}
	test := &Test{Plan: "Expect -> gemini"}
	if err := ValidateTestPlanPerformPhases(suite, test, t.TempDir(), t.TempDir()); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateTestPlanPerformPhases_ParseError(t *testing.T) {
	t.Parallel()
	suite := &Suite{}
	test := &Test{Plan: "@"}
	err := ValidateTestPlanPerformPhases(suite, test, t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Fatalf("got %v", err)
	}
}

func TestPreflightLoadedSuite_NilWorkspace(t *testing.T) {
	t.Parallel()
	if err := PreflightLoadedSuite(nil, "x"); err == nil {
		t.Fatal("expected error")
	}
}

func TestPreflightLoadedSuite_SuiteNotFound(t *testing.T) {
	t.Parallel()
	ws := &Workspace{BaseDir: "/tmp", Suites: map[string]*Suite{}}
	if err := PreflightLoadedSuite(ws, "nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestPreflightLoadedSuite_LoadWorkspaceOK(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.CreateFile(t, tmpDir, "suite/dojo.yaml", `
concurrency: 1
apis:
  g:
    mode: mock
    url: /x
entrypoints:
  w:
    type: http
    path: /hook
`)
	testutil.CreateFile(t, tmpDir, "suite/test_a/test.plan", "Perform -> POST /ok\nExpect -> g")
	testutil.CreateFile(t, tmpDir, "suite/test_a/g_request.json", "{}")

	ws, err := LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}
	if err := PreflightLoadedSuite(ws, "suite"); err != nil {
		t.Fatalf("PreflightLoadedSuite: %v", err)
	}
}

func TestPreflightLoadedSuite_RejectsMissingEvaluatorKey(t *testing.T) {
	t.Setenv("PF_SUITE_KEY_MISSING", "")
	tmpDir := t.TempDir()
	testutil.CreateFile(t, tmpDir, "suite/dojo.yaml", `
concurrency: 1
evaluator:
  provider: gemini
  model: m
  api_key_env: PF_SUITE_KEY_MISSING
apis:
  g:
    mode: mock
    url: /x
entrypoints:
  w:
    type: http
    path: /hook
`)
	testutil.CreateFile(t, tmpDir, "suite/test_a/test.plan", "Perform -> POST /ok\nExpect -> g")
	testutil.CreateFile(t, tmpDir, "suite/test_a/g_request.json", "{}")

	ws, err := LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}
	if err := PreflightLoadedSuite(ws, "suite"); err == nil {
		t.Fatal("expected preflight error for missing API key")
	}
}

func TestPreflightLoadedSuite_WrapsTestNameOnPerformError(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.CreateFile(t, tmpDir, "suite/dojo.yaml", `
concurrency: 1
apis:
  g:
    mode: mock
    url: /x
entrypoints:
  w:
    type: http
    path: /hook
`)
	testutil.CreateFile(t, tmpDir, "suite/test_bad/test.plan", "Perform -> entrypoints/unknown")

	ws, err := LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}
	err = PreflightLoadedSuite(ws, "suite")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "test_bad") {
		t.Fatalf("got %v", err)
	}
}

func TestSplitPlanPhases_TwoPhases(t *testing.T) {
	t.Parallel()
	lines := []ParsedLine{
		{Action: "Perform", Target: "entrypoints/webhook"},
		{Action: "Expect", Target: "gemini"},
		{Action: "Perform", Target: "postgres"},
	}
	phases := SplitPlanPhases(lines)
	if len(phases) != 2 {
		t.Fatalf("got %d phases", len(phases))
	}
	if len(phases[0].Expects) != 1 {
		t.Fatalf("phase 0 expects: %d", len(phases[0].Expects))
	}
}

func TestValidateStartupPlanFixtures_RejectNonExpect(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	ws := &Workspace{}
	suite := &Suite{
		StartupPlan: "Perform -> POST /nope\n",
		APIs:        map[string]APIConfig{"g": {Mode: "mock", URL: "/x"}},
	}
	if err := ValidateStartupPlanFixtures(ws, suite, tmp); err == nil {
		t.Fatal("expected error for Perform in startup.plan")
	}
}

func TestValidateStartupPlanFixtures_WiresAndEval(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "g.json", `{}`)
	ws := &Workspace{}
	suite := &Suite{
		StartupPlan: "Expect -> g -> Request: g.json\n",
		APIs: map[string]APIConfig{
			"g": {Mode: "mock", URL: "/x"},
		},
	}
	if err := ValidateStartupPlanFixtures(ws, suite, tmp); err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	suite2 := &Suite{
		StartupPlan: "Expect -> g -> Request: g.json -> Evaluate Response\n",
		APIs: map[string]APIConfig{
			"g": {Mode: "mock", URL: "/x"},
		},
	}
	if err := ValidateStartupPlanFixtures(ws, suite2, tmp); err == nil {
		t.Fatal("expected error when Evaluate Response is set but eval is empty")
	}
}

func TestValidateTestPlanStatic_UnknownExpectAPI(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "w.json", `{}`)
	suite := &Suite{
		APIs: map[string]APIConfig{
			"g": {Mode: "mock", URL: "/x"},
		},
		Entrypoints: map[string]EntrypointConfig{
			"w": {Type: "http", Path: "/hook"},
		},
	}
	test := &Test{
		Plan: "Perform -> entrypoints/w\nExpect -> not_defined_api",
		APIs: map[string]APIConfig{
			"g": {Mode: "mock", URL: "/x"},
		},
	}
	if err := ValidateTestPlanStatic(suite, test, tmp, tmp); err == nil {
		t.Fatal("expected error for unknown Expect API")
	}
}

func TestValidateTestPlanStatic_EvaluateWithoutEval(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "w.json", `{}`)
	testutil.CreateFile(t, tmp, "g.json", `{}`)
	suite := &Suite{
		APIs: map[string]APIConfig{
			"g": {Mode: "mock", URL: "/x"},
		},
		Entrypoints: map[string]EntrypointConfig{
			"w": {Type: "http", Path: "/hook"},
		},
	}
	test := &Test{
		Plan: "Perform -> entrypoints/w\nExpect -> g -> Request: g.json -> Evaluate Response",
		Eval: "",
		APIs: map[string]APIConfig{
			"g": {Mode: "mock", URL: "/x"},
		},
	}
	if err := ValidateTestPlanStatic(suite, test, tmp, tmp); err == nil {
		t.Fatal("expected error when Evaluate Response is set but eval is empty")
	}
}

func TestValidateTestPlanStatic_InvalidMaxCallsOnExpect(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "w.json", `{}`)
	suite := &Suite{
		APIs: map[string]APIConfig{
			"g": {Mode: "mock", URL: "/x"},
		},
		Entrypoints: map[string]EntrypointConfig{
			"w": {Type: "http", Path: "/hook"},
		},
	}
	test := &Test{
		Plan: "Perform -> entrypoints/w\nExpect -> g -> MaxCalls: nope",
		APIs: map[string]APIConfig{
			"g": {Mode: "mock", URL: "/x"},
		},
	}
	if err := ValidateTestPlanStatic(suite, test, tmp, tmp); err == nil {
		t.Fatal("expected MaxCalls parse error")
	}
}

func TestValidateStartupPlanFixtures_EvaluateOKWithWorkspaceEval(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "g.json", `{}`)
	ws := &Workspace{GlobalEval: "check the payload"}
	suite := &Suite{
		StartupPlan: "Expect -> g -> Request: g.json -> Evaluate Response\n",
		APIs: map[string]APIConfig{
			"g": {Mode: "mock", URL: "/x"},
		},
	}
	if err := ValidateStartupPlanFixtures(ws, suite, tmp); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestValidateStartupPlanFixtures_EvaluateOKWithSuiteEval(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	testutil.CreateFile(t, tmp, "g.json", `{}`)
	ws := &Workspace{}
	suite := &Suite{
		StartupPlan: "Expect -> g -> Request: g.json -> Evaluate Response\n",
		Eval:        "suite rule",
		APIs: map[string]APIConfig{
			"g": {Mode: "mock", URL: "/x"},
		},
	}
	if err := ValidateStartupPlanFixtures(ws, suite, tmp); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}
