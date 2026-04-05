package workspace_test

import (
	"os"
	"path/filepath"
	"testing"
	"dojo/internal/workspace"
)

func TestLoadWorkspace(t *testing.T) {
	tmpDir := t.TempDir()

	createFile(t, tmpDir, "eval.md", "Global Eval Rule")
	
	createFile(t, tmpDir, "tests/dojo.config", `{"concurrency": 40}`)
	createFile(t, tmpDir, "tests/eval.md", "Suite Eval Rule")
	
	createFile(t, tmpDir, "tests/apis/gemini.json", `{"mode": "live", "timeout": "5s", "url": "https://${ENV_API_HOST}", "headers": {"Authorization": "Bearer ${ENV_API_KEY}"}}`)
	createFile(t, tmpDir, "tests/apis/whatsapp.json", `{"mode": "mock", "timeout": "5s", "url": "/v1/messages", "expected_request": {"file": "whatsapp_req.json"}, "default_response": {"code": 200, "file": "whatsapp_resp.json"}}`)
	createFile(t, tmpDir, "tests/whatsapp_req.json", `{"message": "hello"}`)
	createFile(t, tmpDir, "tests/whatsapp_resp.json", `{"status": "ok"}`)
	
	createFile(t, tmpDir, "tests/entrypoints/webhook.json", `{"type": "http", "path": "/trigger", "correlation": {"type": "jsonpath", "target": "payload.id"}}`)
	
	createFile(t, tmpDir, "tests/test_001/test.plan", `
Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> gemini -> Payload: request.json -> Evaluate Response
`)
	createFile(t, tmpDir, "tests/test_001/apis/gemini.json", `{"mode": "mock", "timeout": "10s", "url": "/v1/gemini"}`)
	createFile(t, tmpDir, "tests/test_001/eval.md", "+\nTest Eval Rule")
	
	createFile(t, tmpDir, "tests/test_002/test.plan", "Perform -> entrypoints/webhook -> Payload: in.json")
	createFile(t, tmpDir, "tests/test_002/eval.md", "Override Rule")
	
	createFile(t, tmpDir, "tests/test_003/test.plan", "Perform -> entrypoints/webhook -> Payload: in.json")

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

	if len(suite.APIs) != 2 {
		t.Errorf("Expected 2 suite APIs, got %d", len(suite.APIs))
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

	if suite.Config.Concurrency != 40 {
		t.Errorf("Expected concurrency 40, got %d", suite.Config.Concurrency)
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

func TestLoadWorkspace_PlanDrivenFixtures(t *testing.T) {
	tmpDir := t.TempDir()

	createFile(t, tmpDir, "suite/dojo.config", `{"concurrency":1}`)
	createFile(t, tmpDir, "suite/apis/gemini.json", `{
		"mode": "mock",
		"url": "/v1beta/models/gemini:generateContent"
	}`)
	createFile(t, tmpDir, "suite/apis/whatsapp.json", `{
		"mode": "mock",
		"url": "/v1/messages",
		"default_response": {"code": 200, "body": "{\"ok\":true}"}
	}`)
	createFile(t, tmpDir, "suite/entrypoints/webhook.json", `{
		"type": "http",
		"path": "/trigger"
	}`)

	// test_plan: plan clauses name every fixture explicitly.
	createFile(t, tmpDir, "suite/test_plan/test.plan", `Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> gemini -> Request: gemini_request.json -> Respond: gemini_response.json
Expect -> whatsapp -> Request: whatsapp_request.json`)
	createFile(t, tmpDir, "suite/test_plan/incoming.json", `{"id":"1"}`)
	createFile(t, tmpDir, "suite/test_plan/gemini_request.json", `{"prompt":"hello"}`)
	createFile(t, tmpDir, "suite/test_plan/gemini_response.json", `{"reply":"world"}`)
	createFile(t, tmpDir, "suite/test_plan/whatsapp_request.json", `{"msg":"hi"}`)

	// test_explicit: apis/ override sets expected_request; plan Respond fills the response.
	createFile(t, tmpDir, "suite/test_explicit/test.plan", `Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> gemini -> Respond: gemini_response.json`)
	createFile(t, tmpDir, "suite/test_explicit/incoming.json", `{"id":"2"}`)
	createFile(t, tmpDir, "suite/test_explicit/gemini_response.json", `{"reply":"auto"}`)
	createFile(t, tmpDir, "suite/test_explicit/custom_req.json", `{"prompt":"explicit"}`)
	createFile(t, tmpDir, "suite/test_explicit/apis/gemini.json", `{
		"expected_request": {"file": "custom_req.json"}
	}`)

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

func createFile(t *testing.T, baseDir, path, content string) {
	t.Helper()
	fullPath := filepath.Join(baseDir, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		t.Fatalf("Failed to create dirs for %s: %v", path, err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write file %s: %v", path, err)
	}
}
