package workspace_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dojo/internal/testutil"
	"dojo/internal/workspace"
)

func TestLoadWorkspace(t *testing.T) {
	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "eval.md", "Global Eval Rule")
	testutil.CreateFile(t, tmpDir, "tests/eval.md", "Suite Eval Rule")
	
	testutil.CreateFile(t, tmpDir, "tests/dojo.yaml", `
concurrency: 1
apis:
  media:
    mode: mock
    default_response:
      code: 200
      body: "suite-level-fallback"
  gemini:
    mode: live
    timeout: 5s
    url: "https://${ENV_API_HOST}"
    headers:
      Authorization: "Bearer ${ENV_API_KEY}"
  whatsapp:
    mode: mock
    timeout: 5s
    url: "/v1/messages"
    expected_request:
      file: whatsapp_req.json
    default_response:
      code: 200
      file: whatsapp_resp.json
entrypoints:
  webhook:
    type: http
    path: "/trigger"
`)
	testutil.CreateFile(t, tmpDir, "tests/whatsapp_req.json", `{"message": "hello"}`)
	testutil.CreateFile(t, tmpDir, "tests/whatsapp_resp.json", `{"status": "ok"}`)
	
	testutil.CreateFile(t, tmpDir, "tests/test_001/test.plan", `
Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> gemini -> Payload: request.json -> Evaluate Response
`)
	testutil.CreateFile(t, tmpDir, "tests/test_001/dojo.yaml", `
apis:
  media:
    mode: mock
    default_response:
      code: 200
      body: "suite-level-fallback"
  gemini:
    mode: mock
    timeout: 10s
    url: "/v1/gemini"
`)
	testutil.CreateFile(t, tmpDir, "tests/test_001/eval.md", "+\nTest Eval Rule")
	
	testutil.CreateFile(t, tmpDir, "tests/test_002/test.plan", "Perform -> entrypoints/webhook -> Payload: in.json")
	testutil.CreateFile(t, tmpDir, "tests/test_002/eval.md", "Override Rule")
	
	testutil.CreateFile(t, tmpDir, "tests/test_003/test.plan", "Perform -> entrypoints/webhook -> Payload: in.json")

	t.Setenv("ENV_API_HOST", "api.gemini.com")
	t.Setenv("ENV_API_KEY", "secret123")

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load workspace: %v", err)
	}

	suite, ok := ws.Suites["tests"]
	if !ok {
		t.Fatalf("Expected tests to be loaded")
	}

	if len(suite.APIs) != 3 {
		t.Errorf("Expected 3 suite APIs, got %d", len(suite.APIs))
	}
	if suite.APIs["gemini"].Timeout != "5s" {
		t.Errorf("Expected gemini timeout 5s, got %s", suite.APIs["gemini"].Timeout)
	}
	if suite.APIs["gemini"].URL != "https://api.gemini.com" {
		t.Errorf("Expected gemini URL to be expanded, got %s", suite.APIs["gemini"].URL)
	}
	if suite.APIs["gemini"].Headers["Authorization"] != "Bearer secret123" {
		t.Errorf("Expected gemini header to be expanded")
	}
	if suite.APIs["gemini"].Mode != "live" {
		t.Errorf("Expected gemini global mode 'live', got %s", suite.APIs["gemini"].Mode)
	}

	if len(suite.Entrypoints) != 1 {
		t.Errorf("Expected 1 suite entrypoint, got %d", len(suite.Entrypoints))
	}

	if suite.APIs["whatsapp"].DefaultResponse == nil {
		t.Fatalf("Expected whatsapp default response")
	}

	if suite.Config.Concurrency != 1 {
		t.Errorf("Expected concurrency 1, got %d", suite.Config.Concurrency)
	}

	test, ok := suite.Tests["test_001"]
	if !ok {
		t.Fatalf("Expected test_001 to be loaded")
	}
	
	if test.APIs["gemini"].Timeout != "10s" {
		t.Errorf("Expected test override timeout 10s, got %s", test.APIs["gemini"].Timeout)
	}
	if test.APIs["gemini"].Mode != "mock" {
		t.Errorf("Expected test override mode 'mock', got %s", test.APIs["gemini"].Mode)
	}
	
	expectedEval1 := "Suite Eval Rule\nTest Eval Rule"
	if test.Eval != expectedEval1 {
		t.Errorf("Expected appended eval %q, got %q", expectedEval1, test.Eval)
	}

	test2, ok := suite.Tests["test_002"]
	if !ok {
		t.Fatalf("Expected test_002 to be loaded")
	}
	if test2.Eval != "Override Rule" {
		t.Errorf("Expected overridden eval 'Override Rule', got %q", test2.Eval)
	}
}

func TestLoadWorkspace_ErrWhenSuiteHasNoTests(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.CreateFile(t, tmpDir, "empty_suite/dojo.yaml", `
concurrency: 1
apis:
  x:
    mode: mock
entrypoints:
  w:
    type: http
    path: /
`)
	_, err := workspace.LoadWorkspace(tmpDir)
	if err == nil {
		t.Fatal("expected error when suite has no test_* directories")
	}
	if !strings.Contains(err.Error(), "no tests found") {
		t.Fatalf("expected 'no tests found' in error, got: %v", err)
	}
}

func TestLoadWorkspace_PlanDrivenFixtures(t *testing.T) {
	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "suite/dojo.yaml", `
concurrency: 1
apis:
  media:
    mode: mock
    default_response:
      code: 200
      body: "suite-level-fallback"
  gemini:
    mode: mock
    url: "/v1beta/models/gemini:generateContent"
  whatsapp:
    mode: mock
    url: "/v1/messages"
    default_response:
      code: 200
      body: '{"ok":true}'
entrypoints:
  webhook:
    type: http
    path: "/trigger"
`)

	// test_plan: plan clauses name every fixture explicitly.
	testutil.CreateFile(t, tmpDir, "suite/test_plan/test.plan", `Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> gemini -> Request: gemini_request.json -> Respond: gemini_response.json
Expect -> whatsapp -> Request: whatsapp_request.json`)
	testutil.CreateFile(t, tmpDir, "suite/test_plan/incoming.json", `{"id":"1"}`)
	testutil.CreateFile(t, tmpDir, "suite/test_plan/gemini_request.json", `{"prompt":"hello"}`)
	testutil.CreateFile(t, tmpDir, "suite/test_plan/gemini_response.json", `{"reply":"world"}`)
	testutil.CreateFile(t, tmpDir, "suite/test_plan/whatsapp_request.json", `{"msg":"hi"}`)

	// test_explicit: apis/ override sets expected_request; plan Respond fills the response.
	testutil.CreateFile(t, tmpDir, "suite/test_explicit/test.plan", `Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> gemini -> Respond: gemini_response.json`)
	testutil.CreateFile(t, tmpDir, "suite/test_explicit/incoming.json", `{"id":"2"}`)
	testutil.CreateFile(t, tmpDir, "suite/test_explicit/gemini_response.json", `{"reply":"auto"}`)
	testutil.CreateFile(t, tmpDir, "suite/test_explicit/custom_req.json", `{"prompt":"explicit"}`)
	testutil.CreateFile(t, tmpDir, "suite/test_explicit/dojo.yaml", `
apis:
  media:
    mode: mock
    default_response:
      code: 200
      body: "suite-level-fallback"
  gemini:
    expected_request:
      file: custom_req.json
`)

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}

	suite := ws.Suites["suite"]

	// --- test_plan: fixtures wired from plan clauses ---
	ta := suite.Tests["test_plan"]

	gemini, ok := ta.APIs["gemini"]
	if !ok {
		t.Fatal("test_plan: expected gemini config")
	}
	if gemini.ExpectedRequest == nil || string(gemini.ExpectedRequest.Payload) != `{"prompt":"hello"}` {
		t.Errorf("test_plan gemini expected_request: got %q", payloadStr(gemini.ExpectedRequest))
	}
	if gemini.DefaultResponse == nil || gemini.DefaultResponse.Code != 200 || string(gemini.DefaultResponse.Payload) != `{"reply":"world"}` {
		t.Errorf("test_plan gemini default_response: got code=%d payload=%q", safeCode(gemini.DefaultResponse), payloadStr2(gemini.DefaultResponse))
	}

	wa, ok := ta.APIs["whatsapp"]
	if !ok {
		t.Fatal("test_plan: expected whatsapp config")
	}
	if wa.ExpectedRequest == nil || string(wa.ExpectedRequest.Payload) != `{"msg":"hi"}` {
		t.Errorf("test_plan whatsapp expected_request: got %q", payloadStr(wa.ExpectedRequest))
	}
	if string(wa.DefaultResponse.Payload) != `{"ok":true}` {
		t.Errorf("test_plan whatsapp default_response should be suite's, got %q", string(wa.DefaultResponse.Payload))
	}

	// --- test_explicit: apis/ sets expected_request, plan sets response ---
	te := suite.Tests["test_explicit"]
	geExplicit := te.APIs["gemini"]
	if string(geExplicit.ExpectedRequest.Payload) != `{"prompt":"explicit"}` {
		t.Errorf("test_explicit gemini expected_request: apis/ override should win, got %q", string(geExplicit.ExpectedRequest.Payload))
	}
	if geExplicit.DefaultResponse == nil || string(geExplicit.DefaultResponse.Payload) != `{"reply":"auto"}` {
		t.Errorf("test_explicit gemini default_response: plan Respond should wire it, got %q", payloadStr2(geExplicit.DefaultResponse))
	}
}

func TestLoadWorkspace_DuplicateExpectedRequestAfterWiring(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "suite/dojo.yaml", `
concurrency: 2
apis:
  media:
    mode: mock
    default_response:
      code: 200
      body: "suite-level-fallback"
  stripe:
    mode: mock
    url: "/v1/charge"
    default_response:
      code: 200
      body: "{}"
entrypoints:
  webhook:
    type: http
    path: "/trigger"
`)

	testutil.CreateFile(t, tmpDir, "suite/test_a/test.plan", `Perform -> entrypoints/webhook
Expect -> stripe -> Request: stripe_request.json`)
	testutil.CreateFile(t, tmpDir, "suite/test_a/stripe_request.json", `{"amount":100}`)

	testutil.CreateFile(t, tmpDir, "suite/test_b/test.plan", `Perform -> entrypoints/webhook
Expect -> stripe -> Request: stripe_request.json`)
	testutil.CreateFile(t, tmpDir, "suite/test_b/stripe_request.json", `{"amount":100}`)

	_, err := workspace.LoadWorkspace(tmpDir)
	if err == nil {
		t.Fatal("expected LoadWorkspace to reject duplicate normalized expected requests across tests")
	}
	if !strings.Contains(err.Error(), "duplicate normalized expected request") {
		t.Fatalf("expected 'duplicate normalized expected request' in error, got: %v", err)
	}
}

func payloadStr(ps *workspace.PayloadSpec) string {
	if ps == nil {
		return "<nil>"
	}
	return string(ps.Payload)
}

func safeCode(dr *workspace.DefaultResponse) int {
	if dr == nil {
		return 0
	}
	return dr.Code
}

func payloadStr2(dr *workspace.DefaultResponse) string {
	if dr == nil {
		return "<nil>"
	}
	return string(dr.Payload)
}

func TestLoadWorkspace_TestLevelAPIFileResolution(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "suite/dojo.yaml", `
concurrency: 1
apis:
  media:
    mode: mock
    default_response:
      code: 200
      body: "suite-level-fallback"
  gemini:
    mode: mock
    url: "/v1beta/models/gemini:generateContent"
  whatsapp:
    mode: mock
    url: "/v1/messages"
    default_response:
      code: 200
      body: '{"ok":true}'
entrypoints:
  webhook:
    type: http
    path: "/trigger"
`)

	// Test overrides the media API with a file reference.
	binaryPayload := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
	imgPath := filepath.Join(tmpDir, "suite", "test_img", "photo.jpg")
	if err := os.MkdirAll(filepath.Dir(imgPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imgPath, binaryPayload, 0644); err != nil {
		t.Fatal(err)
	}

	testutil.CreateFile(t, tmpDir, "suite/test_img/dojo.yaml", `
apis:
  media:
    default_response:
      code: 200
      file: "photo.jpg"
      content_type: "image/jpeg"
`)
	testutil.CreateFile(t, tmpDir, "suite/test_img/test.plan", "Perform -> entrypoints/webhook")

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}

	suite := ws.Suites["suite"]
	test := suite.Tests["test_img"]
	mediaCfg, ok := test.APIs["media"]
	if !ok {
		t.Fatal("expected media API in test")
	}

	if !bytes.Equal(mediaCfg.DefaultResponse.Payload, binaryPayload) {
		t.Errorf("test-level file not resolved\n  got:  %v\n  want: %v", mediaCfg.DefaultResponse.Payload, binaryPayload)
	}
	if mediaCfg.DefaultResponse.ContentType != "image/jpeg" {
		t.Errorf("content_type not preserved: got %q", mediaCfg.DefaultResponse.ContentType)
	}
}

func TestLoadWorkspace_TestLevelEntrypointOverride(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "suite/dojo.yaml", `
concurrency: 1
apis:
  media:
    mode: mock
    default_response:
      code: 200
      body: "suite-level-fallback"
  gemini:
    mode: mock
    url: "/v1beta/models/gemini:generateContent"
  whatsapp:
    mode: mock
    url: "/v1/messages"
    default_response:
      code: 200
      body: '{"ok":true}'
entrypoints:
  webhook:
    type: http
    path: "/trigger"
    method: "POST"
    headers:
      Content-Type: "application/json"
      X-Base: "yes"
`)

	testutil.CreateFile(t, tmpDir, "suite/test_base/test.plan", "Perform -> entrypoints/webhook")

	testutil.CreateFile(t, tmpDir, "suite/test_override/dojo.yaml", `
entrypoints:
  webhook:
    headers:
      X-Sig: "abc123"
`)
	testutil.CreateFile(t, tmpDir, "suite/test_override/test.plan", "Perform -> entrypoints/webhook")

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}

	suite := ws.Suites["suite"]

	// test_base should inherit the suite entrypoint.
	if len(suite.Tests["test_base"].Entrypoints) != 1 {
		t.Errorf("test_base: expected 1 entrypoint, got %d", len(suite.Tests["test_base"].Entrypoints))
	}

	// test_override should deep-merge: retain suite-level path, method,
	// Content-Type and X-Base, and add X-Sig.
	overrideEP, ok := suite.Tests["test_override"].Entrypoints["webhook"]
	if !ok {
		t.Fatal("test_override: expected 'webhook' entrypoint override")
	}
	if overrideEP.Path != "/trigger" {
		t.Errorf("path not inherited: got %q, want /trigger", overrideEP.Path)
	}
	if overrideEP.Headers["Content-Type"] != "application/json" {
		t.Errorf("Content-Type not inherited: got %q", overrideEP.Headers["Content-Type"])
	}
	if overrideEP.Headers["X-Base"] != "yes" {
		t.Errorf("X-Base not inherited: got %q", overrideEP.Headers["X-Base"])
	}
	if overrideEP.Headers["X-Sig"] != "abc123" {
		t.Errorf("X-Sig not merged: got %q", overrideEP.Headers["X-Sig"])
	}
}

