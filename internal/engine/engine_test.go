package engine_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dojo/internal/engine"
	"dojo/internal/testutil"
	"dojo/internal/workspace"

	"github.com/jackc/pgproto3/v2"
)

func TestEngineExecution(t *testing.T) {
	sutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/trigger" {
			body, _ := io.ReadAll(r.Body)
			
			stripeURL := os.Getenv("API_STRIPE_URL")
			if stripeURL != "" {
				client := &http.Client{Timeout: 2 * time.Second}
				resp, err := client.Post(stripeURL, "application/json", strings.NewReader(string(body)))
				if err == nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
			}

			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status": "ok"}`))
			return
		}
		http.Error(w, "Not found", http.StatusNotFound)
	}))
	defer sutServer.Close()

	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "test_suite/apis/stripe.json", `{
		"mode": "mock",
		"timeout": "5s",
		"url": "/v1/charge",
		"expected_request": {
			"body": "{\"order_id\": \"ord_123\"}"
		},
		"default_response": {
			"code": 200,
			"body": "{\"status\": \"success\"}"
		}
	}`)
	
	testutil.CreateFile(t, tmpDir, "test_suite/dojo.config", `{"concurrency": 2}`)
	
	testutil.CreateFile(t, tmpDir, "test_suite/entrypoints/webhook.json", `{
		"type": "http",
		"path": "/trigger",
		"url": "`+sutServer.URL+`",
		"expected_response": {
			"body": "{\"status\": \"ok\"}"
		}
	}`)

	testutil.CreateFile(t, tmpDir, "test_suite/test_001/test.plan", `
Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> stripe -> Payload: ""
`)
	testutil.CreateFile(t, tmpDir, "test_suite/test_001/incoming.json", `{"order_id": "ord_123"}`)

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load workspace: %v", err)
	}

	eng := engine.NewEngine(ws)


	if _, err := eng.StartProxies(context.Background(), "test_suite"); err != nil {
		t.Fatalf("Failed to start proxies: %v", err)
	}
	defer eng.StopProxies()

	t.Setenv("API_STRIPE_URL", "http://"+eng.HTTPProxy.Addr()+"/stripe")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	summary, err := eng.RunSuite(ctx, "test_suite", nil)
	if err != nil {
		t.Fatalf("Suite execution failed: %v", err)
	}

	if summary.TotalRuns != 1 {
		t.Errorf("Expected 1 total run, got %d", summary.TotalRuns)
	}
	if summary.Passed != 1 {
		t.Errorf("Expected 1 passed run, got %d", summary.Passed)
	}
	if summary.Failed != 0 {
		t.Errorf("Expected 0 failed runs, got %d. Failures: %v", summary.Failed, summary.Failures)
	}
}

// TestEngineLiveHTTPExpectedResponse asserts that a live HTTP dependency fulfills Expect only after
// the real upstream response matches expected_response (not merely after the outbound request).
func TestEngineLiveHTTPExpectedResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"from_upstream":"live-marker-xyz"}`)); err != nil {
			t.Errorf("upstream write: %v", err)
		}
	}))
	defer upstream.Close()

	sutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/trigger" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			stripeURL := os.Getenv("API_STRIPE_URL")
			if stripeURL != "" {
				client := &http.Client{Timeout: 2 * time.Second}
				resp, err := client.Post(stripeURL, "application/json", strings.NewReader(string(body)))
				if err == nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status": "ok"}`))
			return
		}
		http.Error(w, "Not found", http.StatusNotFound)
	}))
	defer sutServer.Close()

	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "test_suite/apis/stripe.json", `{
		"mode": "live",
		"timeout": "5s",
		"url": "`+upstream.URL+`",
		"expected_request": {
			"body": "{\"order_id\": \"ord_123\"}"
		},
		"expected_response": {
			"body": "live-marker-xyz"
		}
	}`)

	testutil.CreateFile(t, tmpDir, "test_suite/dojo.config", `{"concurrency": 2}`)

	testutil.CreateFile(t, tmpDir, "test_suite/entrypoints/webhook.json", `{
		"type": "http",
		"path": "/trigger",
		"url": "`+sutServer.URL+`",
		"expected_response": {
			"body": "{\"status\": \"ok\"}"
		}
	}`)

	testutil.CreateFile(t, tmpDir, "test_suite/test_001/test.plan", `
Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> stripe -> Payload: ""
`)
	testutil.CreateFile(t, tmpDir, "test_suite/test_001/incoming.json", `{"order_id": "ord_123"}`)

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load workspace: %v", err)
	}

	eng := engine.NewEngine(ws)


	if _, err := eng.StartProxies(context.Background(), "test_suite"); err != nil {
		t.Fatalf("Failed to start proxies: %v", err)
	}
	defer eng.StopProxies()

	t.Setenv("API_STRIPE_URL", "http://"+eng.HTTPProxy.Addr()+"/stripe")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	summary, err := eng.RunSuite(ctx, "test_suite", nil)
	if err != nil {
		t.Fatalf("Suite execution failed: %v", err)
	}

	if summary.Passed != 1 || summary.Failed != 0 {
		t.Fatalf("want 1 passed, 0 failed; got passed=%d failed=%d failures=%v", summary.Passed, summary.Failed, summary.Failures)
	}
}

func TestRegistryUnregister(t *testing.T) {
	t.Parallel()

	reg := engine.NewRegistry()
	active := &engine.ActiveTest{ID: "test-1"}
	reg.Register("test-1", active)

	if _, ok := reg.Lookup("test-1"); !ok {
		t.Fatal("expected test-1 to be registered")
	}

	reg.Unregister("test-1")

	if _, ok := reg.Lookup("test-1"); ok {
		t.Fatal("expected test-1 to be unregistered")
	}

	// Idempotent: calling Unregister again should not panic.
	reg.Unregister("test-1")
}

func TestRegistryCleanupAfterTest(t *testing.T) {
	sutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/trigger" {
			body, _ := io.ReadAll(r.Body)
			stripeURL := os.Getenv("API_STRIPE_URL")
			if stripeURL != "" {
				client := &http.Client{Timeout: 2 * time.Second}
				resp, err := client.Post(stripeURL, "application/json", strings.NewReader(string(body)))
				if err == nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status": "ok"}`))
			return
		}
		http.Error(w, "Not found", http.StatusNotFound)
	}))
	defer sutServer.Close()

	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "test_suite/apis/stripe.json", `{
		"mode": "mock",
		"timeout": "5s",
		"url": "/v1/charge",
		"expected_request": {"body": "{\"order_id\": \"ord_cleanup\"}"},
		"default_response": {"code": 200, "body": "{\"status\": \"success\"}"}
	}`)
	testutil.CreateFile(t, tmpDir, "test_suite/dojo.config", `{"concurrency": 1}`)
	testutil.CreateFile(t, tmpDir, "test_suite/entrypoints/webhook.json", `{
		"type": "http",
		"path": "/trigger",
		"url": "`+sutServer.URL+`",
		"expected_response": {"body": "{\"status\": \"ok\"}"}
	}`)
	testutil.CreateFile(t, tmpDir, "test_suite/test_001/test.plan", `
Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> stripe -> Payload: ""
`)
	testutil.CreateFile(t, tmpDir, "test_suite/test_001/incoming.json", `{"order_id": "ord_cleanup"}`)

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}

	eng := engine.NewEngine(ws)


	if _, err := eng.StartProxies(context.Background(), "test_suite"); err != nil {
		t.Fatalf("start proxies: %v", err)
	}
	defer eng.StopProxies()

	t.Setenv("API_STRIPE_URL", "http://"+eng.HTTPProxy.Addr()+"/stripe")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := eng.RunSuite(ctx, "test_suite", nil); err != nil {
		t.Fatalf("suite execution: %v", err)
	}

	// After suite completes, the registry should have been cleaned up.
	if _, ok := eng.Registry.Lookup("ord_cleanup"); ok {
		t.Fatal("expected registry entry to be cleaned up after test completion")
	}
}

func TestWithLogger(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(h)
	eng := engine.NewEngine(&workspace.Workspace{}, engine.WithLogger(logger))
	_ = eng // verifies WithLogger branch and non-nil logger path
}

func TestProcessRequest_NoActiveSuite(t *testing.T) {
	t.Parallel()
	eng := engine.NewEngine(&workspace.Workspace{})
	m := eng.ProcessRequest("http", "any", []byte("{}"))
	if m.Err == nil || !strings.Contains(m.Err.Error(), "no active suite") {
		t.Fatalf("expected 'no active suite' error, got: %v", m.Err)
	}
}

func TestProcessRequest_APINotFound(t *testing.T) {
	t.Parallel()
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = &workspace.Suite{APIs: map[string]workspace.APIConfig{}}
	m := eng.ProcessRequest("http", "nonexistent", []byte("{}"))
	if m.Err == nil || !strings.Contains(m.Err.Error(), "not found in suite") {
		t.Fatalf("expected 'not found in suite' error, got: %v", m.Err)
	}
}

func TestProcessRequest_PostgresMatchesNormalizedSQL(t *testing.T) {
	t.Parallel()
	suite := &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"pgdb": {Protocol: "postgres", Mode: "mock",
				ExpectedRequest: &workspace.PayloadSpec{Payload: []byte("INSERT INTO t VALUES (1)")},
				DefaultResponse: &workspace.DefaultResponse{Code: 200, Payload: []byte(`{"ok":true}`)},
			},
		},
	}
	active := &engine.ActiveTest{
		ID:    "test_folder",
		Suite: suite,
		Test:  &workspace.Test{APIs: map[string]workspace.APIConfig{}},
		Expectations: map[string][]*engine.Expectation{
			"pgdb": {{Target: "pgdb"}},
		},
	}
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = suite
	eng.Registry.Register("test_folder", active)

	m := eng.ProcessRequest("postgres", "", []byte("  INSERT   INTO   t   VALUES (1)  "))
	if m.Err != nil {
		t.Fatalf("unexpected error: %v", m.Err)
	}
	if !m.IsMock {
		t.Error("expected mock mode for postgres API")
	}
	if m.MatchedID != "test_folder" {
		t.Errorf("MatchedID: got %q", m.MatchedID)
	}
	if !active.Expectations["pgdb"][0].Fulfilled {
		t.Error("expected postgres expectation fulfilled after normalized match")
	}
}

func TestProcessRequest_SubsetMatchSucceeds(t *testing.T) {
	t.Parallel()
	suite := &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"gemini": {Mode: "mock", URL: "/v1/generate",
				ExpectedRequest: &workspace.PayloadSpec{Payload: []byte(`{"contents":[{"role":"user"}]}`)},
				DefaultResponse: &workspace.DefaultResponse{Code: 200, Payload: []byte(`{"ok":true}`)},
			},
		},
	}
	active := &engine.ActiveTest{
		ID:    "test_subset",
		Suite: suite,
		Test:  &workspace.Test{APIs: map[string]workspace.APIConfig{}},
		Expectations: map[string][]*engine.Expectation{
			"gemini": {{Target: "gemini"}},
		},
	}
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = suite
	eng.Registry.Register("test_subset", active)

	actual := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"temperature":0.7},"systemInstruction":{"parts":[{"text":"You are helpful."}]}}`)
	m := eng.ProcessRequest("http", "gemini", actual)
	if m.Err != nil {
		t.Fatalf("unexpected error: %v", m.Err)
	}
	if m.MatchedID != "test_subset" {
		t.Errorf("MatchedID: got %q, want %q", m.MatchedID, "test_subset")
	}
}

func TestProcessRequest_SubsetMismatchFails(t *testing.T) {
	t.Parallel()
	suite := &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"gemini": {Mode: "mock", URL: "/v1/generate",
				ExpectedRequest: &workspace.PayloadSpec{Payload: []byte(`{"contents":[{"role":"user"}],"missing_key":"required"}`)},
				DefaultResponse: &workspace.DefaultResponse{Code: 200, Payload: []byte(`{"ok":true}`)},
			},
		},
	}
	active := &engine.ActiveTest{
		ID:    "test_nomatch",
		Suite: suite,
		Test:  &workspace.Test{APIs: map[string]workspace.APIConfig{}},
		Expectations: map[string][]*engine.Expectation{
			"gemini": {{Target: "gemini"}},
		},
	}
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = suite
	eng.Registry.Register("test_nomatch", active)

	actual := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	m := eng.ProcessRequest("http", "gemini", actual)
	if m.MatchedID != "" {
		t.Errorf("expected no match, got MatchedID=%q", m.MatchedID)
	}
}

func TestProcessRequest_PostgresContainsMatch(t *testing.T) {
	t.Parallel()
	suite := &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"pgdb": {Protocol: "postgres", Mode: "mock",
				ExpectedRequest: &workspace.PayloadSpec{Payload: []byte("INSERT INTO users")},
				DefaultResponse: &workspace.DefaultResponse{Code: 200, Payload: []byte(`{"ok":true}`)},
			},
		},
	}
	active := &engine.ActiveTest{
		ID:    "test_pg_contains",
		Suite: suite,
		Test:  &workspace.Test{APIs: map[string]workspace.APIConfig{}},
		Expectations: map[string][]*engine.Expectation{
			"pgdb": {{Target: "pgdb"}},
		},
	}
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = suite
	eng.Registry.Register("test_pg_contains", active)

	m := eng.ProcessRequest("postgres", "", []byte("INSERT INTO users (id, name) VALUES ('1', 'alice')"))
	if m.Err != nil {
		t.Fatalf("unexpected error: %v", m.Err)
	}
	if m.MatchedID != "test_pg_contains" {
		t.Errorf("MatchedID: got %q, want %q", m.MatchedID, "test_pg_contains")
	}
}

func TestProcessRequest_OrderedMultiExpectations(t *testing.T) {
	t.Parallel()
	suite := &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"gemini": {Mode: "mock", URL: "/v1/generate",
				OrderedExpectations: []workspace.ExpectationSpec{
					{
						ExpectedRequest: &workspace.PayloadSpec{Payload: []byte(`{"role":"intent"}`)},
						Response:        &workspace.DefaultResponse{Code: 200, Payload: []byte(`{"intent":"log_nutrition"}`)},
					},
					{
						ExpectedRequest: &workspace.PayloadSpec{Payload: []byte(`{"role":"conversation"}`)},
						Response:        &workspace.DefaultResponse{Code: 200, Payload: []byte(`{"reply":"Logged!"}`)},
					},
				},
			},
		},
	}
	active := &engine.ActiveTest{
		ID:    "test_multi",
		Suite: suite,
		Test:  &workspace.Test{APIs: map[string]workspace.APIConfig{}},
		Expectations: map[string][]*engine.Expectation{
			"gemini": {{Target: "gemini", Index: 0}, {Target: "gemini", Index: 1}},
		},
	}
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = suite
	eng.Registry.Register("test_multi", active)

	// First call matches intent expectation.
	m1 := eng.ProcessRequest("http", "gemini", []byte(`{"role":"intent","extra":"ignored"}`))
	if m1.Err != nil {
		t.Fatalf("first call: unexpected error: %v", m1.Err)
	}
	if string(m1.MockResponse) != `{"intent":"log_nutrition"}` {
		t.Errorf("first call: got response %q", string(m1.MockResponse))
	}
	if !active.Expectations["gemini"][0].Fulfilled {
		t.Error("first expectation should be fulfilled")
	}
	if active.Expectations["gemini"][1].Fulfilled {
		t.Error("second expectation should NOT be fulfilled yet")
	}

	// Second call matches conversation expectation.
	m2 := eng.ProcessRequest("http", "gemini", []byte(`{"role":"conversation","context":"stuff"}`))
	if m2.Err != nil {
		t.Fatalf("second call: unexpected error: %v", m2.Err)
	}
	if string(m2.MockResponse) != `{"reply":"Logged!"}` {
		t.Errorf("second call: got response %q", string(m2.MockResponse))
	}
	if !active.Expectations["gemini"][1].Fulfilled {
		t.Error("second expectation should be fulfilled")
	}
}

func TestProcessRequest_MockCodeDefaultsTo200(t *testing.T) {
	t.Parallel()
	suite := &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"stripe": {Mode: "mock",
				DefaultResponse: &workspace.DefaultResponse{Code: 0, Payload: []byte(`{"ok":true}`)},
			},
		},
	}
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = suite
	m := eng.ProcessRequest("http", "stripe", []byte("{}"))
	if m.Err != nil {
		t.Fatalf("unexpected: %v", m.Err)
	}
	if !m.IsMock {
		t.Error("expected mock")
	}
	if m.MockCode != 200 {
		t.Errorf("expected 200, got %d", m.MockCode)
	}
}

func TestProcessResponse_NilActiveSuite(t *testing.T) {
	t.Parallel()
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ProcessResponse("http", "id", "api", nil, nil) // must not panic
}

func TestProcessResponse_EmptyMatchedID(t *testing.T) {
	t.Parallel()
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = &workspace.Suite{}
	eng.ProcessResponse("http", "", "api", nil, nil) // must not panic
}

func TestProcessResponse_MatchedIDNotInRegistry(t *testing.T) {
	t.Parallel()
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = &workspace.Suite{}
	eng.ProcessResponse("http", "unknown", "api", nil, nil) // must not panic
}

func TestProcessResponse_HTTPLiveMatch(t *testing.T) {
	t.Parallel()
	suite := &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"ext": {Mode: "live", URL: "http://example.com",
				ExpectedResponse: &workspace.PayloadSpec{Payload: []byte("success-marker")},
			},
		},
	}
	active := &engine.ActiveTest{
		ID:   "t1",
		Test: &workspace.Test{APIs: map[string]workspace.APIConfig{}},
		Suite: suite,
		Expectations: map[string][]*engine.Expectation{
			"ext": {{Target: "ext"}},
		},
	}
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = suite
	eng.Registry.Register("t1", active)

	eng.ProcessResponse("http", "t1", "ext", nil, []byte(`{"data":"success-marker"}`))
	if !active.Expectations["ext"][0].Fulfilled {
		t.Error("expected fulfilled after matching response")
	}
}

func TestProcessResponse_HTTPLiveMismatch(t *testing.T) {
	t.Parallel()
	suite := &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"ext": {Mode: "live", URL: "http://example.com",
				ExpectedResponse: &workspace.PayloadSpec{Payload: []byte("expected-marker")},
			},
		},
	}
	active := &engine.ActiveTest{
		ID:   "t1",
		Test: &workspace.Test{APIs: map[string]workspace.APIConfig{}},
		Suite: suite,
		Expectations: map[string][]*engine.Expectation{
			"ext": {{Target: "ext"}},
		},
	}
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = suite
	eng.Registry.Register("t1", active)

	eng.ProcessResponse("http", "t1", "ext", nil, []byte(`{"data":"wrong"}`))
	if !active.Expectations["ext"][0].Fulfilled {
		t.Error("expected fulfilled (with error) on mismatch")
	}
	if active.Expectations["ext"][0].Error == nil {
		t.Fatal("expected MismatchError on mismatch, got nil")
	}
	if !strings.Contains(active.Expectations["ext"][0].Error.Error(), "live response mismatch") {
		t.Errorf("expected 'live response mismatch' in error, got: %v", active.Expectations["ext"][0].Error)
	}
}

func TestProcessResponse_HTTPMockSkipped(t *testing.T) {
	t.Parallel()
	suite := &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"mockapi": {Mode: "mock"},
		},
	}
	active := &engine.ActiveTest{
		ID:   "t1",
		Test: &workspace.Test{APIs: map[string]workspace.APIConfig{}},
		Suite: suite,
		Expectations: map[string][]*engine.Expectation{
			"mockapi": {{Target: "mockapi"}},
		},
	}
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = suite
	eng.Registry.Register("t1", active)

	eng.ProcessResponse("http", "t1", "mockapi", nil, []byte(`anything`))
	if active.Expectations["mockapi"][0].Fulfilled {
		t.Error("mock mode should not fulfill via ProcessResponse")
	}
}

func TestProcessResponse_PostgresExpectedResponseMatch(t *testing.T) {
	t.Parallel()
	suite := &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"pgdb": {Protocol: "postgres", Mode: "live", URL: "postgres://host/db",
				ExpectedResponse: &workspace.PayloadSpec{Payload: []byte("INSERT 0 1")},
			},
		},
	}
	active := &engine.ActiveTest{
		ID:   "t1",
		Test: &workspace.Test{APIs: map[string]workspace.APIConfig{}},
		Suite: suite,
		Expectations: map[string][]*engine.Expectation{
			"pgdb": {{Target: "pgdb"}},
		},
	}
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = suite
	eng.Registry.Register("t1", active)

	eng.ProcessResponse("postgres", "t1", "", nil, []byte("INSERT 0 1"))
	if !active.Expectations["pgdb"][0].Fulfilled {
		t.Error("expected pgdb expectation fulfilled")
	}
}

func TestProcessResponse_PostgresNoExpectedResponse(t *testing.T) {
	t.Parallel()
	suite := &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"pgdb": {Protocol: "postgres", Mode: "live", URL: "postgres://host/db",
				OrderedExpectations: []workspace.ExpectationSpec{{
					ExpectedRequest: &workspace.PayloadSpec{Payload: []byte("INSERT INTO t")},
				}},
			},
		},
	}
	active := &engine.ActiveTest{
		ID:   "t1",
		Test: &workspace.Test{APIs: map[string]workspace.APIConfig{}},
		Suite: suite,
		Expectations: map[string][]*engine.Expectation{
			"pgdb": {{Target: "pgdb"}},
		},
	}
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = suite
	eng.Registry.Register("t1", active)

	rfq, _ := (&pgproto3.ReadyForQuery{TxStatus: 'I'}).Encode(nil)
	eng.ProcessResponse("postgres", "t1", "", []byte("INSERT INTO t VALUES (1)"), rfq)
	if !active.Expectations["pgdb"][0].Fulfilled {
		t.Error("expected pgdb expectation fulfilled (no expected_response)")
	}
}

func TestProcessRequest_PostgresLiveDefersFullfillment(t *testing.T) {
	t.Parallel()
	suite := &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"pgdb": {Protocol: "postgres", Mode: "live", URL: "postgres://host/db",
				ExpectedRequest: &workspace.PayloadSpec{Payload: []byte("INSERT INTO t")},
				OrderedExpectations: []workspace.ExpectationSpec{{
					ExpectedRequest: &workspace.PayloadSpec{Payload: []byte("INSERT INTO t")},
				}},
			},
		},
	}
	active := &engine.ActiveTest{
		ID:    "t1",
		Suite: suite,
		Test:  &workspace.Test{APIs: map[string]workspace.APIConfig{}},
		Expectations: map[string][]*engine.Expectation{
			"pgdb": {{Target: "pgdb"}},
		},
	}
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = suite
	eng.Registry.Register("t1", active)

	m := eng.ProcessRequest("postgres", "", []byte("INSERT INTO t VALUES (1)"))
	if m.Err != nil {
		t.Fatalf("unexpected error: %v", m.Err)
	}
	if m.MatchedID != "t1" {
		t.Fatalf("expected MatchedID=t1, got %q", m.MatchedID)
	}
	if active.Expectations["pgdb"][0].Fulfilled {
		t.Error("live postgres should defer fulfillment to ProcessResponse")
	}
}

func TestProcessResponse_PostgresErrorResponse(t *testing.T) {
	t.Parallel()
	suite := &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"pgdb": {Protocol: "postgres", Mode: "live", URL: "postgres://host/db",
				OrderedExpectations: []workspace.ExpectationSpec{{
					ExpectedRequest: &workspace.PayloadSpec{Payload: []byte("INSERT INTO t")},
				}},
			},
		},
	}
	active := &engine.ActiveTest{
		ID:   "t1",
		Test: &workspace.Test{APIs: map[string]workspace.APIConfig{}},
		Suite: suite,
		Expectations: map[string][]*engine.Expectation{
			"pgdb": {{Target: "pgdb"}},
		},
	}
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = suite
	eng.Registry.Register("t1", active)

	er := &pgproto3.ErrorResponse{Message: "table does not exist"}
	data, _ := er.Encode(nil)
	eng.ProcessResponse("postgres", "t1", "", []byte("INSERT INTO t VALUES (1)"), data)
	if !active.Expectations["pgdb"][0].Fulfilled {
		t.Error("expected fulfilled (with error) after ErrorResponse")
	}
	if active.Expectations["pgdb"][0].Error == nil {
		t.Error("expected non-nil error for ErrorResponse")
	}
}

func TestProcessResponse_PostgresMismatch(t *testing.T) {
	t.Parallel()
	suite := &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"pgdb": {Protocol: "postgres", Mode: "live", URL: "postgres://host/db",
				ExpectedResponse: &workspace.PayloadSpec{Payload: []byte("INSERT 0 1")},
			},
		},
	}
	active := &engine.ActiveTest{
		ID:   "t1",
		Test: &workspace.Test{APIs: map[string]workspace.APIConfig{}},
		Suite: suite,
		Expectations: map[string][]*engine.Expectation{
			"pgdb": {{Target: "pgdb"}},
		},
	}
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = suite
	eng.Registry.Register("t1", active)

	eng.ProcessResponse("postgres", "t1", "", nil, []byte("SELECT 1"))
	if active.Expectations["pgdb"][0].Fulfilled {
		t.Error("expected NOT fulfilled on mismatch")
	}
}

func TestProcessResponse_PostgresTestOverride(t *testing.T) {
	t.Parallel()
	suite := &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"pgdb": {Protocol: "postgres", Mode: "live", URL: "postgres://host/db",
				ExpectedResponse: &workspace.PayloadSpec{Payload: []byte("wrong")},
			},
		},
	}
	// Test-level API overrides suite-level
	testAPIs := map[string]workspace.APIConfig{
		"pgdb": {Protocol: "postgres", Mode: "live",
			ExpectedResponse: &workspace.PayloadSpec{Payload: []byte("INSERT 0 1")},
		},
	}
	active := &engine.ActiveTest{
		ID:   "t1",
		Test: &workspace.Test{APIs: testAPIs},
		Suite: suite,
		Expectations: map[string][]*engine.Expectation{
			"pgdb": {{Target: "pgdb"}},
		},
	}
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = suite
	eng.Registry.Register("t1", active)

	eng.ProcessResponse("postgres", "t1", "", nil, []byte("INSERT 0 1"))
	if !active.Expectations["pgdb"][0].Fulfilled {
		t.Error("expected pgdb fulfilled via test-level override")
	}
}

// TestEngineImplicitCorrelation verifies routing by normalized expected request
// body alone (no correlation config): notify traffic matches test-level expected_request.
func TestEngineImplicitCorrelation(t *testing.T) {
	var httpProxyAddr string

	sutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/trigger" {
			http.NotFound(w, r)
			return
		}
		notifyURL := "http://" + httpProxyAddr + "/notify/v1/send"
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Post(notifyURL, "application/json", strings.NewReader(`{"customer":"cust_42","message":"done"}`))
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer sutServer.Close()

	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "suite/dojo.config", `{"concurrency":1}`)
	testutil.CreateFile(t, tmpDir, "suite/apis/notify.json", `{
		"mode": "mock",
		"url": "/v1/send",
		"default_response": {"code": 200, "body": "{\"ok\":true}"}
	}`)
	testutil.CreateFile(t, tmpDir, "suite/entrypoints/webhook.json", fmt.Sprintf(`{
		"type": "http",
		"path": "/trigger",
		"url": "%s"
	}`, sutServer.URL))

	testutil.CreateFile(t, tmpDir, "suite/test_correlate/test.plan", `
Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> notify
`)
	testutil.CreateFile(t, tmpDir, "suite/test_correlate/incoming.json", `{"order_id":"ord_100"}`)
	testutil.CreateFile(t, tmpDir, "suite/test_correlate/apis/notify.json", `{
		"expected_request": {"body": "{\"customer\":\"cust_42\",\"message\":\"done\"}"}
	}`)

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}

	eng := engine.NewEngine(ws)


	if _, err := eng.StartProxies(context.Background(), "suite"); err != nil {
		t.Fatalf("StartProxies: %v", err)
	}
	defer eng.StopProxies()

	httpProxyAddr = eng.HTTPProxy.Addr()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	summary, err := eng.RunSuite(ctx, "suite", nil)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}

	if summary.Passed != 1 || summary.Failed != 0 {
		for _, f := range summary.Failures {
			t.Logf("failure: %s — %s", f.TestName, f.Reason)
		}
		t.Fatalf("want 1 passed 0 failed, got passed=%d failed=%d", summary.Passed, summary.Failed)
	}
}

func TestStartProxies_SuiteNotFound(t *testing.T) {
	t.Parallel()
	eng := engine.NewEngine(&workspace.Workspace{Suites: map[string]*workspace.Suite{}})
	_, err := eng.StartProxies(context.Background(), "nonexistent")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected suite not found error, got: %v", err)
	}
}

func TestRunSuite_NoActiveSuite(t *testing.T) {
	t.Parallel()
	eng := engine.NewEngine(&workspace.Workspace{})
	_, err := eng.RunSuite(context.Background(), "any", nil)
	if err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("expected not initialized error, got: %v", err)
	}
}

func TestMarkFulfilled_AllDone(t *testing.T) {
	t.Parallel()
	at := &engine.ActiveTest{
		ID: "t",
		Expectations: map[string][]*engine.Expectation{
			"a": {{Target: "a"}},
			"b": {{Target: "b"}},
		},
	}
	at.MarkFulfilled("a", 0, nil)
	if !at.Expectations["a"][0].Fulfilled {
		t.Error("a not fulfilled")
	}
	at.MarkFulfilled("b", 0, nil)
	if !at.Expectations["b"][0].Fulfilled {
		t.Error("b not fulfilled")
	}
}

func TestMarkFulfilled_WithError(t *testing.T) {
	t.Parallel()
	at := &engine.ActiveTest{
		ID: "t",
		Expectations: map[string][]*engine.Expectation{
			"a": {{Target: "a"}},
		},
	}
	testErr := fmt.Errorf("payload mismatch")
	at.MarkFulfilled("a", 0, testErr)
	if !at.Expectations["a"][0].Fulfilled {
		t.Error("a not fulfilled")
	}
	if at.Expectations["a"][0].Error != testErr {
		t.Errorf("expected error %v, got %v", testErr, at.Expectations["a"][0].Error)
	}
}

func TestMarkFulfilled_UnknownAPI(t *testing.T) {
	t.Parallel()
	at := &engine.ActiveTest{
		ID: "t",
		Expectations: map[string][]*engine.Expectation{
			"a": {{Target: "a"}},
		},
	}
	at.MarkFulfilled("unknown", 0, nil) // must not panic
	if at.Expectations["a"][0].Fulfilled {
		t.Error("a should not be fulfilled")
	}
}

// TestProcessRequest_LiveHTTPEvalDefersToResponse verifies that for live HTTP
// APIs with RequiresEval, ProcessRequest does NOT mark the expectation fulfilled
// and does NOT call Evaluate on the request payload.
func TestProcessRequest_LiveHTTPEvalDefersToResponse(t *testing.T) {
	t.Parallel()
	suite := &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"gemini": {Mode: "live", URL: "https://example.com",
				ExpectedRequest: &workspace.PayloadSpec{Payload: []byte(`{"prompt":"hello"}`)},
			},
		},
	}
	active := &engine.ActiveTest{
		ID:   "t1",
		Test: &workspace.Test{APIs: map[string]workspace.APIConfig{}},
		Suite: suite,
		Expectations: map[string][]*engine.Expectation{
			"gemini": {{Target: "gemini", RequiresEval: true}},
		},
	}
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = suite
	eng.Registry.Register("t1", active)

	m := eng.ProcessRequest("http", "gemini", []byte(`{"prompt":"hello"}`))
	if m.Err != nil {
		t.Fatalf("unexpected error: %v", m.Err)
	}
	if m.MatchedID != "t1" {
		t.Fatalf("expected match t1, got %q", m.MatchedID)
	}
	if active.Expectations["gemini"][0].Fulfilled {
		t.Error("live HTTP + RequiresEval must NOT be fulfilled in ProcessRequest; should defer to ProcessResponse")
	}
}

// TestProcessResponse_HTTPLiveEvalFailsWithoutConfig verifies that for live HTTP
// APIs with RequiresEval and no evaluator config, ProcessResponse still marks the
// expectation as fulfilled (done) but records a non-nil error because the AI
// evaluation cannot run without a configured provider.
func TestProcessResponse_HTTPLiveEvalFailsWithoutConfig(t *testing.T) {
	t.Parallel()
	suite := &workspace.Suite{
		APIs: map[string]workspace.APIConfig{
			"gemini": {Mode: "live", URL: "https://example.com"},
		},
	}
	active := &engine.ActiveTest{
		ID:   "t1",
		Test: &workspace.Test{APIs: map[string]workspace.APIConfig{}},
		Suite: suite,
		Expectations: map[string][]*engine.Expectation{
			"gemini": {{Target: "gemini", RequiresEval: true}},
		},
	}
	eng := engine.NewEngine(&workspace.Workspace{})
	eng.ActiveSuite = suite
	eng.Registry.Register("t1", active)

	eng.ProcessResponse("http", "t1", "gemini", nil, []byte(`{"candidates":[{"content":"response"}]}`))
	if !active.Expectations["gemini"][0].Fulfilled {
		t.Error("expected gemini expectation to be marked fulfilled (done)")
	}
	if active.Expectations["gemini"][0].Error == nil {
		t.Fatal("expected non-nil error because evaluator config is missing")
	}
	if got := active.Expectations["gemini"][0].Error.Error(); !strings.Contains(got, "no eval.md rule found") {
		t.Errorf("unexpected error message: %s", got)
	}
}

// TestBinaryFixturePayload proves that a non-JSON file (here a tiny JPEG) used
// in `Payload: image.jpg` is loaded from disk and sent byte-for-byte to the SUT
// entry point. Before the generalised file-extension check in execute.go this
// test fails because the engine treats "image.jpg" as a literal string.
func TestBinaryFixturePayload(t *testing.T) {
	// Minimal valid JPEG: SOI + APP0 (JFIF) + DQT + SOF0 + DHT + SOS + EOI.
	// Built by hand so the test has no external dependencies.
	jpegData := []byte{
		0xFF, 0xD8, // SOI
		0xFF, 0xE0, 0x00, 0x10, // APP0 length=16
		0x4A, 0x46, 0x49, 0x46, 0x00, // "JFIF\0"
		0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, // version, density
		0xFF, 0xDB, 0x00, 0x43, 0x00, // DQT length=67
		0x08, 0x06, 0x06, 0x07, 0x06, 0x05, 0x08, 0x07,
		0x07, 0x07, 0x09, 0x09, 0x08, 0x0A, 0x0C, 0x14,
		0x0D, 0x0C, 0x0B, 0x0B, 0x0C, 0x19, 0x12, 0x13,
		0x0F, 0x14, 0x1D, 0x1A, 0x1F, 0x1E, 0x1D, 0x1A,
		0x1C, 0x1C, 0x20, 0x24, 0x2E, 0x27, 0x20, 0x22,
		0x2C, 0x23, 0x1C, 0x1C, 0x28, 0x37, 0x29, 0x2C,
		0x30, 0x31, 0x34, 0x34, 0x34, 0x1F, 0x27, 0x39,
		0x3D, 0x38, 0x32, 0x3C, 0x2E, 0x33, 0x34, 0x32,
		0xFF, 0xC0, 0x00, 0x0B, 0x08, // SOF0 length=11, 8-bit
		0x00, 0x01, 0x00, 0x01, 0x01, 0x01, 0x11, 0x00, // 1x1, 1 component
		0xFF, 0xC4, 0x00, 0x1F, 0x00, // DHT length=31, DC table 0
		0x00, 0x01, 0x05, 0x01, 0x01, 0x01, 0x01, 0x01,
		0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0A, 0x0B,
		0xFF, 0xDA, 0x00, 0x08, 0x01, 0x01, 0x00, 0x00, 0x3F, 0x00, // SOS
		0x7B, 0x40, // minimal scan data
		0xFF, 0xD9, // EOI
	}

	var capturedBody []byte
	var httpProxyAddr string

	sutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/upload" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		capturedBody = body

		// Forward a deterministic JSON call so the Expect line is fulfilled.
		visionURL := "http://" + httpProxyAddr + "/vision/v1/analyze"
		resp, err := http.Post(visionURL, "application/json",
			strings.NewReader(`{"image_hash":"abc123"}`))
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"received"}`))
	}))
	defer sutServer.Close()

	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "suite/dojo.config", `{"concurrency":1}`)
	testutil.CreateFile(t, tmpDir, "suite/entrypoints/upload.json", fmt.Sprintf(`{
		"type": "http",
		"path": "/upload",
		"url": %q
	}`, sutServer.URL))
	testutil.CreateFile(t, tmpDir, "suite/apis/vision.json", `{
		"mode": "mock",
		"url": "/v1/analyze",
		"default_response": {"code": 200, "body": "{\"label\":\"cat\"}"}
	}`)

	// Write the binary JPEG fixture.
	jpegPath := filepath.Join(tmpDir, "suite", "test_image", "image.jpg")
	if err := os.MkdirAll(filepath.Dir(jpegPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jpegPath, jpegData, 0644); err != nil {
		t.Fatal(err)
	}

	testutil.CreateFile(t, tmpDir, "suite/test_image/test.plan", `
Perform -> entrypoints/upload -> Payload: image.jpg

Expect -> vision -> Request: vision_request.json
`)
	testutil.CreateFile(t, tmpDir, "suite/test_image/vision_request.json",
		`{"image_hash":"abc123"}`)

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}

	eng := engine.NewEngine(ws)


	if _, err := eng.StartProxies(context.Background(), "suite"); err != nil {
		t.Fatalf("StartProxies: %v", err)
	}
	defer eng.StopProxies()

	httpProxyAddr = eng.HTTPProxy.Addr()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	summary, err := eng.RunSuite(ctx, "suite", nil)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}

	if summary.Passed != 1 || summary.Failed != 0 {
		for _, f := range summary.Failures {
			t.Logf("failure: %s — %s", f.TestName, f.Reason)
		}
		t.Fatalf("want 1 passed 0 failed, got passed=%d failed=%d", summary.Passed, summary.Failed)
	}

	if !bytes.Equal(capturedBody, jpegData) {
		t.Fatalf("SUT received %d bytes, want %d bytes (binary JPEG mismatch)",
			len(capturedBody), len(jpegData))
	}
}

func TestUnsupportedEntrypointType(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "suite/dojo.config", `{"concurrency":1}`)
	testutil.CreateFile(t, tmpDir, "suite/entrypoints/queue.json", `{
		"type": "amqp",
		"path": "/messages"
	}`)
	testutil.CreateFile(t, tmpDir, "suite/apis/payments.json", `{
		"mode": "mock",
		"url": "/v1/pay",
		"expected_request": {"body": "{}"},
		"default_response": {"code": 200, "body": "{}"}
	}`)
	testutil.CreateFile(t, tmpDir, "suite/test_msg/test.plan", "Perform -> entrypoints/queue\nExpect -> payments")

	_, err := workspace.LoadWorkspace(tmpDir)
	if err == nil {
		t.Fatal("expected LoadWorkspace to reject unknown entrypoint type")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Fatalf("expected 'unknown type' in error, got: %v", err)
	}
}

func TestSUTCrashPropagatesMidTest(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "suite/dojo.config", fmt.Sprintf(`{
		"concurrency": 1,
		"sut_command": "go run ./sut.go",
		"timeouts": {"sut_startup": "10s", "perform": "2s"}
	}`))

	// SUT that listens on :18923, responds once, then crashes.
	testutil.CreateFile(t, tmpDir, "suite/sut.go", `package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	http.HandleFunc("/trigger", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
		go func() {
			time.Sleep(100 * time.Millisecond)
			fmt.Fprintln(os.Stderr, "crashing now")
			os.Exit(42)
		}()
	})
	http.ListenAndServe(":18923", nil)
}
`)

	testutil.CreateFile(t, tmpDir, "suite/entrypoints/webhook.json", `{
		"type": "http",
		"path": "/trigger",
		"url": "http://127.0.0.1:18923"
	}`)
	testutil.CreateFile(t, tmpDir, "suite/apis/external.json", `{
		"mode": "mock",
		"url": "/v1/call",
		"expected_request": {"body": "{\"will_never_arrive\":true}"},
		"default_response": {"code": 200, "body": "{}"}
	}`)
	testutil.CreateFile(t, tmpDir, "suite/test_crash/test.plan", "Perform -> entrypoints/webhook\nExpect -> external")

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}

	eng := engine.NewEngine(ws)
	if _, err := eng.StartProxies(context.Background(), "suite"); err != nil {
		t.Fatalf("StartProxies: %v", err)
	}
	defer eng.StopProxies()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	summary, err := eng.RunSuite(ctx, "suite", nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}

	if summary.Failed != 1 {
		t.Fatalf("want 1 failure, got passed=%d failed=%d", summary.Passed, summary.Failed)
	}

	reason := summary.Failures[0].Reason
	if !strings.Contains(reason, "SUT") && !strings.Contains(reason, "crashed") {
		t.Fatalf("expected SUT crash reason, got: %s", reason)
	}

	if elapsed > 10*time.Second {
		t.Fatalf("test should have failed fast on SUT crash, but took %v", elapsed)
	}
}

func TestMismatchErrorPopulatesStructuredFields(t *testing.T) {
	t.Parallel()

	sutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"unexpected":"value"}`))
	}))
	defer sutServer.Close()

	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "suite/dojo.config", `{"concurrency":1}`)
	testutil.CreateFile(t, tmpDir, "suite/entrypoints/webhook.json", fmt.Sprintf(`{
		"type": "http",
		"path": "/",
		"url": %q,
		"expected_response": {"body": "{\"expected\":\"match\"}"}
	}`, sutServer.URL))
	testutil.CreateFile(t, tmpDir, "suite/test_mismatch/test.plan", "Perform -> entrypoints/webhook")

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}

	eng := engine.NewEngine(ws)
	if _, err := eng.StartProxies(context.Background(), "suite"); err != nil {
		t.Fatalf("StartProxies: %v", err)
	}
	defer eng.StopProxies()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	summary, err := eng.RunSuite(ctx, "suite", nil)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}

	if summary.Failed != 1 {
		t.Fatalf("want 1 failure, got %d", summary.Failed)
	}

	f := summary.Failures[0]
	if f.Expected == "" {
		t.Error("TestFailure.Expected should be populated")
	}
	if f.Actual == "" {
		t.Error("TestFailure.Actual should be populated")
	}
	if !strings.Contains(f.Expected, "expected") {
		t.Errorf("Expected field should contain expected value, got: %s", f.Expected)
	}
	if !strings.Contains(f.Actual, "unexpected") {
		t.Errorf("Actual field should contain actual response, got: %s", f.Actual)
	}

	r := summary.Results[0]
	if r.Expected == "" || r.Actual == "" {
		t.Error("TestResult.Expected/Actual should be populated for mismatches")
	}
}

func TestEnvVarExpansionInMockResponse(t *testing.T) {
	t.Parallel()

	var gotBody string
	sutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/trigger" {
			apiURL := os.Getenv("API_MEDIA_URL")
			if apiURL != "" {
				client := &http.Client{Timeout: 2 * time.Second}
				resp, err := client.Get(apiURL + "/lookup")
				if err == nil {
					body, _ := io.ReadAll(resp.Body)
					gotBody = string(body)
					resp.Body.Close()
				}
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer sutServer.Close()

	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "suite/dojo.config", `{"concurrency":1}`)
	testutil.CreateFile(t, tmpDir, "suite/apis/media.json", `{
		"mode": "mock",
		"default_response": {"code": 200, "body": "{\"url\": \"$API_MEDIA_URL/download/file.jpg\"}"}
	}`)
	testutil.CreateFile(t, tmpDir, "suite/entrypoints/webhook.json", fmt.Sprintf(`{
		"type": "http",
		"path": "/trigger",
		"url": %q
	}`, sutServer.URL))
	testutil.CreateFile(t, tmpDir, "suite/test_expand/test.plan", "Perform -> entrypoints/webhook -> Payload: incoming.json")
	testutil.CreateFile(t, tmpDir, "suite/test_expand/incoming.json", `{}`)

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}

	eng := engine.NewEngine(ws)
	if _, err := eng.StartProxies(context.Background(), "suite"); err != nil {
		t.Fatalf("StartProxies: %v", err)
	}
	defer eng.StopProxies()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	summary, err := eng.RunSuite(ctx, "suite", nil)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	if summary.Failed != 0 {
		t.Fatalf("want 0 failures, got %d: %v", summary.Failed, summary.Failures)
	}

	expectedPrefix := "http://" + eng.HTTPProxy.Addr() + "/media"
	if !strings.Contains(gotBody, expectedPrefix) {
		t.Errorf("mock response body was not expanded\n  got:  %s\n  want to contain: %s", gotBody, expectedPrefix)
	}
}

func TestMockResponseContentType(t *testing.T) {
	var gotContentType string
	var mediaURL string

	sutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/trigger" {
			if mediaURL != "" {
				client := &http.Client{Timeout: 2 * time.Second}
				resp, err := client.Get(mediaURL + "/photo.jpg")
				if err == nil {
					gotContentType = resp.Header.Get("Content-Type")
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer sutServer.Close()

	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "suite/dojo.config", `{"concurrency":1}`)
	testutil.CreateFile(t, tmpDir, "suite/apis/media.json", `{
		"mode": "mock",
		"default_response": {
			"code": 200,
			"body": "binary-image-data",
			"content_type": "image/jpeg"
		}
	}`)
	testutil.CreateFile(t, tmpDir, "suite/entrypoints/webhook.json", fmt.Sprintf(`{
		"type": "http",
		"path": "/trigger",
		"url": %q
	}`, sutServer.URL))
	testutil.CreateFile(t, tmpDir, "suite/test_ct/test.plan", "Perform -> entrypoints/webhook -> Payload: incoming.json")
	testutil.CreateFile(t, tmpDir, "suite/test_ct/incoming.json", `{}`)

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}

	eng := engine.NewEngine(ws)
	if _, err := eng.StartProxies(context.Background(), "suite"); err != nil {
		t.Fatalf("StartProxies: %v", err)
	}
	defer eng.StopProxies()

	mediaURL = "http://" + eng.HTTPProxy.Addr() + "/media"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	summary, err := eng.RunSuite(ctx, "suite", nil)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	if summary.Failed != 0 {
		t.Fatalf("want 0 failures, got %d: %v", summary.Failed, summary.Failures)
	}
	if gotContentType != "image/jpeg" {
		t.Errorf("expected Content-Type image/jpeg, got %q", gotContentType)
	}
}

func TestExpectStatus(t *testing.T) {
	sutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(200)
			w.Write([]byte("OK"))
		case "/forbidden":
			w.WriteHeader(403)
			w.Write([]byte("Forbidden"))
		case "/teapot":
			w.WriteHeader(418)
			w.Write([]byte("I'm a teapot"))
		default:
			w.WriteHeader(404)
		}
	}))
	defer sutServer.Close()

	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "suite/dojo.config", `{"concurrency":1}`)
	testutil.CreateFile(t, tmpDir, "suite/entrypoints/health.json", fmt.Sprintf(`{
		"type": "http", "path": "/health", "url": %q
	}`, sutServer.URL))
	testutil.CreateFile(t, tmpDir, "suite/entrypoints/forbidden.json", fmt.Sprintf(`{
		"type": "http", "path": "/forbidden", "url": %q
	}`, sutServer.URL))
	testutil.CreateFile(t, tmpDir, "suite/entrypoints/teapot.json", fmt.Sprintf(`{
		"type": "http", "path": "/teapot", "url": %q
	}`, sutServer.URL))

	// test_ok: 200 matches 200 -> pass
	testutil.CreateFile(t, tmpDir, "suite/test_ok/test.plan",
		`Perform -> entrypoints/health -> ExpectStatus: "200"`)
	// test_mismatch: 403 != 200 -> fail
	testutil.CreateFile(t, tmpDir, "suite/test_mismatch/test.plan",
		`Perform -> entrypoints/forbidden -> ExpectStatus: "200"`)
	// test_418: 418 matches 418 -> pass (proves 4xx status can be expected)
	testutil.CreateFile(t, tmpDir, "suite/test_418/test.plan",
		`Perform -> entrypoints/teapot -> ExpectStatus: "418"`)

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}

	eng := engine.NewEngine(ws)
	if _, err := eng.StartProxies(context.Background(), "suite"); err != nil {
		t.Fatalf("StartProxies: %v", err)
	}
	defer eng.StopProxies()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	summary, err := eng.RunSuite(ctx, "suite", nil)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}

	if summary.Passed != 2 {
		t.Errorf("expected 2 passed, got %d", summary.Passed)
	}
	if summary.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", summary.Failed)
	}

	var found bool
	for _, f := range summary.Failures {
		if f.TestName == "test_mismatch" {
			found = true
			if !strings.Contains(f.Reason, "expected HTTP status 200, got 403") {
				t.Errorf("unexpected failure reason: %s", f.Reason)
			}
			if f.Expected != "200" || f.Actual != "403" {
				t.Errorf("structured fields wrong: expected=%q actual=%q", f.Expected, f.Actual)
			}
		}
	}
	if !found {
		t.Error("test_mismatch should have failed but didn't appear in failures")
	}
}

func TestFollowRedirects(t *testing.T) {
	sutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			http.Redirect(w, r, "https://external.example.com/oauth?state=abc", http.StatusTemporaryRedirect)
		default:
			w.WriteHeader(404)
		}
	}))
	defer sutServer.Close()

	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "suite/dojo.config", `{"concurrency":1}`)

	// follow_redirects: false — should capture the 307 without following
	testutil.CreateFile(t, tmpDir, "suite/entrypoints/auth.json", fmt.Sprintf(`{
		"type": "http", "method": "GET", "path": "/auth",
		"url": %q, "follow_redirects": false
	}`, sutServer.URL))

	testutil.CreateFile(t, tmpDir, "suite/test_redirect/test.plan",
		`Perform -> entrypoints/auth -> ExpectStatus: "307"`)

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}

	eng := engine.NewEngine(ws)
	if _, err := eng.StartProxies(context.Background(), "suite"); err != nil {
		t.Fatalf("StartProxies: %v", err)
	}
	defer eng.StopProxies()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	summary, err := eng.RunSuite(ctx, "suite", nil)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	if summary.Failed != 0 {
		t.Fatalf("expected 0 failures, got %d: %v", summary.Failed, summary.Failures)
	}
}

func TestExpectTimeout_PerAPITimeout(t *testing.T) {
	t.Parallel()
	sutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer sutServer.Close()

	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "suite/dojo.config", `{"concurrency":1}`)
	testutil.CreateFile(t, tmpDir, "suite/entrypoints/webhook.json", fmt.Sprintf(`{
		"type":"http", "path":"/trigger", "url":%q
	}`, sutServer.URL))
	testutil.CreateFile(t, tmpDir, "suite/apis/external.json", `{
		"mode":"mock", "url":"/v1/call", "timeout":"500ms",
		"expected_request":{"body":"{\"will_never_arrive\":true}"},
		"default_response":{"code":200,"body":"{}"}
	}`)
	testutil.CreateFile(t, tmpDir, "suite/test_timeout/test.plan",
		"Perform -> entrypoints/webhook\nExpect -> external")

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}

	eng := engine.NewEngine(ws)
	if _, err := eng.StartProxies(context.Background(), "suite"); err != nil {
		t.Fatalf("StartProxies: %v", err)
	}
	defer eng.StopProxies()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	summary, err := eng.RunSuite(ctx, "suite", nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("expected 1 failure, got %d", summary.Failed)
	}
	if !strings.Contains(summary.Failures[0].Reason, "timed out") {
		t.Fatalf("expected timeout error, got: %s", summary.Failures[0].Reason)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("test took %s; per-API timeout of 500ms should have kicked in much sooner", elapsed)
	}
}

func TestExpectTimeout_GlobalFallback(t *testing.T) {
	t.Parallel()
	sutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer sutServer.Close()

	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "suite/dojo.config", `{
		"concurrency":1,
		"timeouts":{"expect":"500ms"}
	}`)
	testutil.CreateFile(t, tmpDir, "suite/entrypoints/webhook.json", fmt.Sprintf(`{
		"type":"http", "path":"/trigger", "url":%q
	}`, sutServer.URL))
	testutil.CreateFile(t, tmpDir, "suite/apis/external.json", `{
		"mode":"mock", "url":"/v1/call",
		"expected_request":{"body":"{\"will_never_arrive\":true}"},
		"default_response":{"code":200,"body":"{}"}
	}`)
	testutil.CreateFile(t, tmpDir, "suite/test_timeout/test.plan",
		"Perform -> entrypoints/webhook\nExpect -> external")

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}

	eng := engine.NewEngine(ws)
	if _, err := eng.StartProxies(context.Background(), "suite"); err != nil {
		t.Fatalf("StartProxies: %v", err)
	}
	defer eng.StopProxies()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	summary, err := eng.RunSuite(ctx, "suite", nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("expected 1 failure, got %d", summary.Failed)
	}
	if !strings.Contains(summary.Failures[0].Reason, "timed out") {
		t.Fatalf("expected timeout error, got: %s", summary.Failures[0].Reason)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("test took %s; global expect timeout of 500ms should have kicked in much sooner", elapsed)
	}
}

func TestExpectTimeout_IndependentFailure(t *testing.T) {
	t.Parallel()

	var proxyAddr string
	sutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/trigger" {
			client := &http.Client{Timeout: 2 * time.Second}
			resp, err := client.Post("http://"+proxyAddr+"/fulfilled",
				"application/json", strings.NewReader(`{"match":"yes"}`))
			if err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			w.WriteHeader(200)
			return
		}
		http.Error(w, "Not found", 404)
	}))
	defer sutServer.Close()

	tmpDir := t.TempDir()

	testutil.CreateFile(t, tmpDir, "suite/dojo.config", `{
		"concurrency":1,
		"timeouts":{"expect":"500ms"}
	}`)
	testutil.CreateFile(t, tmpDir, "suite/entrypoints/webhook.json", fmt.Sprintf(`{
		"type":"http", "path":"/trigger", "url":%q
	}`, sutServer.URL))
	testutil.CreateFile(t, tmpDir, "suite/apis/fulfilled.json", `{
		"mode":"mock", "url":"/v1/call",
		"expected_request":{"body":"{\"match\":\"yes\"}"},
		"default_response":{"code":200,"body":"{}"}
	}`)
	testutil.CreateFile(t, tmpDir, "suite/apis/missed.json", `{
		"mode":"mock", "url":"/v1/other",
		"expected_request":{"body":"{\"will_never_arrive\":true}"},
		"default_response":{"code":200,"body":"{}"}
	}`)
	testutil.CreateFile(t, tmpDir, "suite/test_mixed/test.plan",
		"Perform -> entrypoints/webhook\nExpect -> fulfilled\nExpect -> missed")

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}

	eng := engine.NewEngine(ws)
	if _, err := eng.StartProxies(context.Background(), "suite"); err != nil {
		t.Fatalf("StartProxies: %v", err)
	}
	defer eng.StopProxies()

	proxyAddr = eng.HTTPProxy.Addr()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	summary, err := eng.RunSuite(ctx, "suite", nil)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("expected 1 failure, got %d", summary.Failed)
	}
	reason := summary.Failures[0].Reason
	if !strings.Contains(reason, "missed") {
		t.Fatalf("expected failure to mention 'missed' API, got: %s", reason)
	}
	if !strings.Contains(reason, "timed out") {
		t.Fatalf("expected timeout error, got: %s", reason)
	}
}
