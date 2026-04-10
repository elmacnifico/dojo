package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateAPIConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		cfg        APIConfig
		wantErr    bool
		errContain string
	}{
		{"empty mode", APIConfig{Mode: ""}, true, "must have mode"},
		{"invalid mode", APIConfig{Mode: "passthrough"}, true, "must have mode"},
		{"live no url", APIConfig{Mode: "live", URL: ""}, true, "no URL"},
		{"live invalid url", APIConfig{Mode: "live", URL: "/path/only"}, true, "invalid URL"},
		{"live valid url", APIConfig{Mode: "live", URL: "https://api.example.com"}, false, ""},
		{"live defaults protocol to http", APIConfig{Mode: "live", URL: "https://api.example.com"}, false, ""},
		{"mock url with domain", APIConfig{Mode: "mock", URL: "https://api.example.com"}, true, "mock but URL contains a domain"},
		{"mock path only url", APIConfig{Mode: "mock", URL: "/v1/charge"}, false, ""},
		{"mock empty url", APIConfig{Mode: "mock", URL: ""}, false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := tc.cfg
			err := validateAPIConfig("testapi", &cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tc.errContain) {
					t.Fatalf("error %q should contain %q", err.Error(), tc.errContain)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}

	// Verify protocol defaulting
	t.Run("protocol defaults to http", func(t *testing.T) {
		t.Parallel()
		cfg := APIConfig{Mode: "live", URL: "https://api.example.com", Protocol: ""}
		if err := validateAPIConfig("x", &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.Protocol != "http" {
			t.Errorf("expected protocol 'http', got %q", cfg.Protocol)
		}
	})
}

func TestResolvePayload(t *testing.T) {
	t.Parallel()

	t.Run("expected request body", func(t *testing.T) {
		t.Parallel()
		cfg := &APIConfig{ExpectedRequest: &PayloadSpec{Body: "hello"}}
		if err := resolvePayload(cfg, "", ""); err != nil {
			t.Fatal(err)
		}
		if string(cfg.ExpectedRequest.Payload) != "hello" {
			t.Errorf("got %q", string(cfg.ExpectedRequest.Payload))
		}
	})

	t.Run("expected request file from primary", func(t *testing.T) {
		t.Parallel()
		primary := t.TempDir()
		if err := os.WriteFile(filepath.Join(primary, "req.json"), []byte(`{"ok":1}`), 0644); err != nil {
			t.Fatal(err)
		}
		cfg := &APIConfig{ExpectedRequest: &PayloadSpec{File: "req.json"}}
		if err := resolvePayload(cfg, primary, ""); err != nil {
			t.Fatal(err)
		}
		if string(cfg.ExpectedRequest.Payload) != `{"ok":1}` {
			t.Errorf("got %q", string(cfg.ExpectedRequest.Payload))
		}
	})

	t.Run("expected request file fallback", func(t *testing.T) {
		t.Parallel()
		primary := t.TempDir()
		fallback := t.TempDir()
		if err := os.WriteFile(filepath.Join(fallback, "req.json"), []byte(`{"fb":1}`), 0644); err != nil {
			t.Fatal(err)
		}
		cfg := &APIConfig{ExpectedRequest: &PayloadSpec{File: "req.json"}}
		if err := resolvePayload(cfg, primary, fallback); err != nil {
			t.Fatal(err)
		}
		if string(cfg.ExpectedRequest.Payload) != `{"fb":1}` {
			t.Errorf("got %q", string(cfg.ExpectedRequest.Payload))
		}
	})

	t.Run("expected request file not found", func(t *testing.T) {
		t.Parallel()
		cfg := &APIConfig{ExpectedRequest: &PayloadSpec{File: "missing.json"}}
		if err := resolvePayload(cfg, t.TempDir(), t.TempDir()); err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("expected response body", func(t *testing.T) {
		t.Parallel()
		cfg := &APIConfig{ExpectedResponse: &PayloadSpec{Body: "resp"}}
		if err := resolvePayload(cfg, "", ""); err != nil {
			t.Fatal(err)
		}
		if string(cfg.ExpectedResponse.Payload) != "resp" {
			t.Errorf("got %q", string(cfg.ExpectedResponse.Payload))
		}
	})

	t.Run("default response body", func(t *testing.T) {
		t.Parallel()
		cfg := &APIConfig{DefaultResponse: &DefaultResponse{Body: "def"}}
		if err := resolvePayload(cfg, "", ""); err != nil {
			t.Fatal(err)
		}
		if string(cfg.DefaultResponse.Payload) != "def" {
			t.Errorf("got %q", string(cfg.DefaultResponse.Payload))
		}
	})

	t.Run("default response file from primary", func(t *testing.T) {
		t.Parallel()
		primary := t.TempDir()
		if err := os.WriteFile(filepath.Join(primary, "resp.json"), []byte(`{"d":1}`), 0644); err != nil {
			t.Fatal(err)
		}
		cfg := &APIConfig{DefaultResponse: &DefaultResponse{File: "resp.json"}}
		if err := resolvePayload(cfg, primary, ""); err != nil {
			t.Fatal(err)
		}
		if string(cfg.DefaultResponse.Payload) != `{"d":1}` {
			t.Errorf("got %q", string(cfg.DefaultResponse.Payload))
		}
	})

	t.Run("all nil returns nil", func(t *testing.T) {
		t.Parallel()
		cfg := &APIConfig{}
		if err := resolvePayload(cfg, "", ""); err != nil {
			t.Fatal(err)
		}
	})
}

func TestExpandEntrypointConfig(t *testing.T) {
	t.Setenv("DOJO_TEST_HOST", "myhost.com")
	t.Setenv("DOJO_TEST_PATH", "/api/v2")
	t.Setenv("DOJO_TEST_TOKEN", "secret")

	cfg := &EntrypointConfig{
		URL:     "https://${DOJO_TEST_HOST}",
		Path:    "${DOJO_TEST_PATH}/trigger",
		Headers: map[string]string{"Authorization": "Bearer ${DOJO_TEST_TOKEN}"},
	}
	expandEntrypointConfig(cfg)

	if cfg.URL != "https://myhost.com" {
		t.Errorf("URL: got %q", cfg.URL)
	}
	if cfg.Path != "/api/v2/trigger" {
		t.Errorf("Path: got %q", cfg.Path)
	}
	if cfg.Headers["Authorization"] != "Bearer secret" {
		t.Errorf("Authorization: got %q", cfg.Headers["Authorization"])
	}
}

func TestExpandEntrypointConfig_NilHeaders(t *testing.T) {
	t.Parallel()
	cfg := &EntrypointConfig{URL: "http://localhost", Path: "/x"}
	expandEntrypointConfig(cfg) // must not panic
}

func TestWireFixturesFromPlan(t *testing.T) {
	t.Parallel()

	t.Run("wires request and respond from plan clauses", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "gemini_request.json"), []byte(`{"p":"req"}`), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "gemini_response.json"), []byte(`{"p":"resp"}`), 0644); err != nil {
			t.Fatal(err)
		}

		suite := &Suite{APIs: map[string]APIConfig{
			"gemini": {Mode: "mock", URL: "/v1/gen"},
		}}
		test := &Test{APIs: make(map[string]APIConfig)}
		doc := &ParsedDocument{Lines: []ParsedLine{
			{Action: "Perform", Target: "entrypoints/webhook"},
			{Action: "Expect", Target: "gemini", Clauses: []ParsedClause{
				{Key: "Request", Value: strPtr("gemini_request.json")},
				{Key: "Respond", Value: strPtr("gemini_response.json")},
			}},
		}}

		if err := wireFixturesFromPlan(doc, test, suite, dir, ""); err != nil {
			t.Fatal(err)
		}
		cfg := test.APIs["gemini"]
		if cfg.ExpectedRequest == nil || string(cfg.ExpectedRequest.Payload) != `{"p":"req"}` {
			t.Errorf("expected_request: got %q", string(cfg.ExpectedRequest.Payload))
		}
		if cfg.DefaultResponse == nil || string(cfg.DefaultResponse.Payload) != `{"p":"resp"}` {
			t.Errorf("default_response: got %q", string(cfg.DefaultResponse.Payload))
		}
	})

	t.Run("wires sql fixture for postgres", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "pg_insert.sql"), []byte("INSERT INTO users VALUES ('a')"), 0644); err != nil {
			t.Fatal(err)
		}

		suite := &Suite{APIs: map[string]APIConfig{
			"postgres": {Mode: "live", Protocol: "postgres", URL: "postgres://localhost/db"},
		}}
		test := &Test{APIs: make(map[string]APIConfig)}
		doc := &ParsedDocument{Lines: []ParsedLine{
			{Action: "Perform", Target: "entrypoints/webhook"},
			{Action: "Expect", Target: "postgres", Clauses: []ParsedClause{
				{Key: "Request", Value: strPtr("pg_insert.sql")},
			}},
		}}

		if err := wireFixturesFromPlan(doc, test, suite, dir, ""); err != nil {
			t.Fatal(err)
		}
		cfg := test.APIs["postgres"]
		if string(cfg.ExpectedRequest.Payload) != "INSERT INTO users VALUES ('a')" {
			t.Errorf("got %q", string(cfg.ExpectedRequest.Payload))
		}
	})

	t.Run("copies suite config when test has no override", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "wa_req.json"), []byte(`{"m":"hi"}`), 0644); err != nil {
			t.Fatal(err)
		}

		suite := &Suite{APIs: map[string]APIConfig{
			"whatsapp": {Mode: "mock", URL: "/v1/msg", DefaultResponse: &DefaultResponse{Code: 200, Body: `{"ok":true}`}},
		}}
		test := &Test{APIs: make(map[string]APIConfig)}
		doc := &ParsedDocument{Lines: []ParsedLine{
			{Action: "Perform", Target: "entrypoints/webhook"},
			{Action: "Expect", Target: "whatsapp", Clauses: []ParsedClause{
				{Key: "Request", Value: strPtr("wa_req.json")},
			}},
		}}

		if err := wireFixturesFromPlan(doc, test, suite, dir, ""); err != nil {
			t.Fatal(err)
		}
		cfg := test.APIs["whatsapp"]
		if cfg.Mode != "mock" || cfg.URL != "/v1/msg" {
			t.Errorf("suite config not inherited: mode=%q url=%q", cfg.Mode, cfg.URL)
		}
		if string(cfg.ExpectedRequest.Payload) != `{"m":"hi"}` {
			t.Errorf("expected_request: got %q", string(cfg.ExpectedRequest.Payload))
		}
	})

	t.Run("errors on unknown API", func(t *testing.T) {
		t.Parallel()
		suite := &Suite{APIs: map[string]APIConfig{}}
		test := &Test{APIs: make(map[string]APIConfig)}
		doc := &ParsedDocument{Lines: []ParsedLine{
			{Action: "Perform", Target: "entrypoints/webhook"},
			{Action: "Expect", Target: "unknown", Clauses: []ParsedClause{
				{Key: "Request", Value: strPtr("req.json")},
			}},
		}}

		err := wireFixturesFromPlan(doc, test, suite, t.TempDir(), "")
		if err == nil || !strings.Contains(err.Error(), "not defined in apis/") {
			t.Errorf("expected error about undefined API, got %v", err)
		}
	})

	t.Run("respond clause preserves existing response code", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "resp.json"), []byte(`{"ok":1}`), 0644); err != nil {
			t.Fatal(err)
		}

		suite := &Suite{APIs: map[string]APIConfig{
			"gemini": {Mode: "mock", URL: "/v1", DefaultResponse: &DefaultResponse{Code: 201}},
		}}
		test := &Test{APIs: make(map[string]APIConfig)}
		doc := &ParsedDocument{Lines: []ParsedLine{
			{Action: "Perform", Target: "entrypoints/webhook"},
			{Action: "Expect", Target: "gemini", Clauses: []ParsedClause{
				{Key: "Respond", Value: strPtr("resp.json")},
			}},
		}}

		if err := wireFixturesFromPlan(doc, test, suite, dir, ""); err != nil {
			t.Fatal(err)
		}
		if test.APIs["gemini"].DefaultResponse.Code != 201 {
			t.Errorf("code should be preserved from suite, got %d", test.APIs["gemini"].DefaultResponse.Code)
		}
	})

	t.Run("deep merges json fixture from suite and test dirs", func(t *testing.T) {
		t.Parallel()
		testDir := t.TempDir()
		suiteDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(suiteDir, "gemini_request.json"), []byte(`{"base":"val","shared":"old"}`), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(testDir, "gemini_request.json"), []byte(`{"shared":"new","extra":true}`), 0644); err != nil {
			t.Fatal(err)
		}

		suite := &Suite{APIs: map[string]APIConfig{
			"gemini": {Mode: "mock", URL: "/v1"},
		}}
		test := &Test{APIs: make(map[string]APIConfig)}
		doc := &ParsedDocument{Lines: []ParsedLine{
			{Action: "Perform", Target: "entrypoints/webhook"},
			{Action: "Expect", Target: "gemini", Clauses: []ParsedClause{
				{Key: "Request", Value: strPtr("gemini_request.json")},
			}},
		}}

		if err := wireFixturesFromPlan(doc, test, suite, testDir, suiteDir); err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(test.APIs["gemini"].ExpectedRequest.Payload, &m); err != nil {
			t.Fatal(err)
		}
		if m["base"] != "val" {
			t.Errorf("suite base key missing: %v", m)
		}
		if m["shared"] != "new" {
			t.Errorf("test override should win: %v", m)
		}
		if m["extra"] != true {
			t.Errorf("test extra key missing: %v", m)
		}
	})
}

func strPtr(s string) *string { return &s }

func TestValidateSuiteConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		cfg        DojoConfig
		wantErr    bool
		errContain string
	}{
		{
			name:    "concurrency zero defaults to 1",
			cfg:     DojoConfig{Concurrency: 0},
			wantErr: false,
		},
		{
			name:    "concurrency negative defaults to 1",
			cfg:     DojoConfig{Concurrency: -5},
			wantErr: false,
		},
		{
			name:    "concurrency positive passes",
			cfg:     DojoConfig{Concurrency: 4},
			wantErr: false,
		},
		{
			name: "evaluator bad provider",
			cfg: DojoConfig{
				Concurrency: 1,
				Evaluator:   &EvaluatorConfig{Provider: "llama", Model: "v1", APIKeyEnv: "KEY"},
			},
			wantErr:    true,
			errContain: "evaluator provider must be one of",
		},
		{
			name: "evaluator empty model",
			cfg: DojoConfig{
				Concurrency: 1,
				Evaluator:   &EvaluatorConfig{Provider: "gemini", Model: "", APIKeyEnv: "KEY"},
			},
			wantErr:    true,
			errContain: "evaluator model must not be empty",
		},
		{
			name: "evaluator empty api_key_env",
			cfg: DojoConfig{
				Concurrency: 1,
				Evaluator:   &EvaluatorConfig{Provider: "openai", Model: "gpt-4", APIKeyEnv: ""},
			},
			wantErr:    true,
			errContain: "evaluator api_key_env must not be empty",
		},
		{
			name: "evaluator valid",
			cfg: DojoConfig{
				Concurrency: 1,
				Evaluator:   &EvaluatorConfig{Provider: "anthropic", Model: "claude-3", APIKeyEnv: "ANTHROPIC_KEY"},
			},
			wantErr: false,
		},
		{
			name: "negative timeout",
			cfg: DojoConfig{
				Concurrency: 1,
				Timeouts:    TimeoutConfig{SUTStartup: Duration{-5 * time.Second}},
			},
			wantErr:    true,
			errContain: "must not be negative",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := tc.cfg
			err := validateSuiteConfig("test-suite", &cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tc.errContain) {
					t.Fatalf("error %q should contain %q", err.Error(), tc.errContain)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}

	t.Run("concurrency clamped to 1", func(t *testing.T) {
		t.Parallel()
		cfg := DojoConfig{Concurrency: -3}
		if err := validateSuiteConfig("s", &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.Concurrency != 1 {
			t.Errorf("expected concurrency clamped to 1, got %d", cfg.Concurrency)
		}
	})
}

func TestValidateEntrypointConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		cfg        EntrypointConfig
		wantErr    bool
		errContain string
	}{
		{"empty type", EntrypointConfig{Type: ""}, true, "type must not be empty"},
		{"unknown type", EntrypointConfig{Type: "grpc"}, true, "unknown type"},
		{"valid http", EntrypointConfig{Type: "http"}, false, ""},
		{"valid HTTP uppercase", EntrypointConfig{Type: "HTTP"}, false, ""},
		{"whitespace type", EntrypointConfig{Type: "  "}, true, "type must not be empty"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := tc.cfg
			err := validateEntrypointConfig("webhook", &cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tc.errContain) {
					t.Fatalf("error %q should contain %q", err.Error(), tc.errContain)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestCopyAPIConfig(t *testing.T) {
	t.Parallel()

	src := APIConfig{
		Mode: "mock",
		URL:  "/v1/test",
		Headers: map[string]string{
			"Authorization": "Bearer token",
		},
		ExpectedRequest:  &PayloadSpec{Body: "req", Payload: []byte("req-payload")},
		ExpectedResponse: &PayloadSpec{Body: "eresp", Payload: []byte("eresp-payload")},
		DefaultResponse:  &DefaultResponse{Code: 200, Body: "resp", Payload: []byte("resp-payload")},
	}

	dst := CopyAPIConfig(src)

	// Mutate source pointers to prove independence.
	src.ExpectedRequest.Body = "mutated"
	src.ExpectedRequest.Payload[0] = 'X'
	src.ExpectedResponse.Payload[0] = 'Y'
	src.DefaultResponse.Body = "mutated"
	src.DefaultResponse.Payload[0] = 'Z'
	src.Headers["Authorization"] = "mutated"

	if dst.ExpectedRequest.Body != "req" {
		t.Errorf("ExpectedRequest not deep-copied: got %q", dst.ExpectedRequest.Body)
	}
	if string(dst.ExpectedRequest.Payload) != "req-payload" {
		t.Errorf("ExpectedRequest.Payload not deep-copied: got %q", dst.ExpectedRequest.Payload)
	}
	if string(dst.ExpectedResponse.Payload) != "eresp-payload" {
		t.Errorf("ExpectedResponse.Payload not deep-copied: got %q", dst.ExpectedResponse.Payload)
	}
	if dst.DefaultResponse.Body != "resp" {
		t.Errorf("DefaultResponse not deep-copied: got %q", dst.DefaultResponse.Body)
	}
	if string(dst.DefaultResponse.Payload) != "resp-payload" {
		t.Errorf("DefaultResponse.Payload not deep-copied: got %q", dst.DefaultResponse.Payload)
	}
	if dst.Headers["Authorization"] != "Bearer token" {
		t.Errorf("Headers not deep-copied: got %q", dst.Headers["Authorization"])
	}
}

func TestExpandAPIConfig(t *testing.T) {
	t.Setenv("DOJO_TEST_URL", "https://api.stripe.com")
	t.Setenv("DOJO_TEST_KEY", "sk_test_123")

	cfg := &APIConfig{
		URL:     "${DOJO_TEST_URL}",
		Headers: map[string]string{"Authorization": "Bearer ${DOJO_TEST_KEY}"},
	}
	expandAPIConfig(cfg)

	if cfg.URL != "https://api.stripe.com" {
		t.Errorf("URL: got %q", cfg.URL)
	}
	if cfg.Headers["Authorization"] != "Bearer sk_test_123" {
		t.Errorf("Authorization: got %q", cfg.Headers["Authorization"])
	}
}

func TestDeepMergeJSON(t *testing.T) {
	t.Parallel()

	t.Run("merges nested objects", func(t *testing.T) {
		t.Parallel()
		base := []byte(`{"a":1,"nested":{"x":10,"y":20}}`)
		overlay := []byte(`{"b":2,"nested":{"y":99,"z":30}}`)
		got, err := deepMergeJSON(base, overlay)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(got, &m); err != nil {
			t.Fatal(err)
		}
		if m["a"] != float64(1) {
			t.Errorf("base key 'a' missing: %v", m)
		}
		if m["b"] != float64(2) {
			t.Errorf("overlay key 'b' missing: %v", m)
		}
		nested := m["nested"].(map[string]any)
		if nested["x"] != float64(10) {
			t.Errorf("nested base key 'x' missing: %v", nested)
		}
		if nested["y"] != float64(99) {
			t.Errorf("nested 'y' should be overridden to 99: %v", nested)
		}
		if nested["z"] != float64(30) {
			t.Errorf("nested overlay key 'z' missing: %v", nested)
		}
	})

	t.Run("overlay array replaces base array", func(t *testing.T) {
		t.Parallel()
		base := []byte(`{"items":[1,2,3]}`)
		overlay := []byte(`{"items":[99]}`)
		got, err := deepMergeJSON(base, overlay)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(got, &m); err != nil {
			t.Fatal(err)
		}
		items := m["items"].([]any)
		if len(items) != 1 || items[0] != float64(99) {
			t.Errorf("array should be replaced entirely: %v", items)
		}
	})

	t.Run("non-object base returns overlay", func(t *testing.T) {
		t.Parallel()
		got, err := deepMergeJSON([]byte(`"plain string"`), []byte(`{"a":1}`))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != `{"a":1}` {
			t.Errorf("expected overlay, got %s", got)
		}
	})

	t.Run("non-object overlay returns overlay", func(t *testing.T) {
		t.Parallel()
		got, err := deepMergeJSON([]byte(`{"a":1}`), []byte(`"plain string"`))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != `"plain string"` {
			t.Errorf("expected overlay, got %s", got)
		}
	})
}

func TestResolveFile_DeepMerge(t *testing.T) {
	t.Parallel()

	t.Run("merges when both dirs have file", func(t *testing.T) {
		t.Parallel()
		testDir := t.TempDir()
		suiteDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(suiteDir, "f.json"), []byte(`{"base":"val","shared":"old"}`), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(testDir, "f.json"), []byte(`{"shared":"new","extra":true}`), 0644); err != nil {
			t.Fatal(err)
		}

		got, err := resolveFile("f.json", testDir, suiteDir)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(got, &m); err != nil {
			t.Fatal(err)
		}
		if m["base"] != "val" {
			t.Errorf("suite-level 'base' key should survive: %v", m)
		}
		if m["shared"] != "new" {
			t.Errorf("test-level 'shared' should override: %v", m)
		}
		if m["extra"] != true {
			t.Errorf("test-level 'extra' should appear: %v", m)
		}
	})

	t.Run("returns test only when suite missing", func(t *testing.T) {
		t.Parallel()
		testDir := t.TempDir()
		suiteDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(testDir, "f.json"), []byte(`{"test":1}`), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := resolveFile("f.json", testDir, suiteDir)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != `{"test":1}` {
			t.Errorf("got %s", got)
		}
	})

	t.Run("returns suite only when test missing", func(t *testing.T) {
		t.Parallel()
		testDir := t.TempDir()
		suiteDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(suiteDir, "f.json"), []byte(`{"suite":1}`), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := resolveFile("f.json", testDir, suiteDir)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != `{"suite":1}` {
			t.Errorf("got %s", got)
		}
	})

	t.Run("non-json falls back to test file", func(t *testing.T) {
		t.Parallel()
		testDir := t.TempDir()
		suiteDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(suiteDir, "q.sql"), []byte("SELECT 1"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(testDir, "q.sql"), []byte("SELECT 2"), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := resolveFile("q.sql", testDir, suiteDir)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "SELECT 2" {
			t.Errorf("non-JSON should return overlay as-is: got %s", got)
		}
	})
}

func TestNormalizeSQL(t *testing.T) {
	t.Parallel()
	got := NormalizeSQL("  SELECT  1\nFROM  t  ;  ")
	want := "SELECT 1 FROM t"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNormalizeHTTPBody_CanonicalJSON(t *testing.T) {
	t.Parallel()
	a := NormalizeHTTPBody([]byte(`{"b":2,"a":1}`))
	b := NormalizeHTTPBody([]byte(`{"a":1,"b":2}`))
	if a != b {
		t.Errorf("equivalent JSON should normalize equal:\n%q\n%q", a, b)
	}
}

func TestJSONSubsetMatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		actual   string
		expected string
		want     bool
	}{
		{"exact match", `{"a":1,"b":2}`, `{"a":1,"b":2}`, true},
		{"subset match", `{"a":1,"b":2,"c":3}`, `{"a":1}`, true},
		{"nested subset", `{"a":{"x":1,"y":2},"b":3}`, `{"a":{"x":1}}`, true},
		{"missing key fails", `{"a":1}`, `{"a":1,"b":2}`, false},
		{"value mismatch fails", `{"a":1}`, `{"a":2}`, false},
		{"type mismatch fails", `{"a":"1"}`, `{"a":1}`, false},
		{"array exact", `[1,2,3]`, `[1,2,3]`, true},
		{"array prefix", `[1,2,3]`, `[1,2]`, true},
		{"array too long fails", `[1,2]`, `[1,2,3]`, false},
		{"array element mismatch", `[1,2,3]`, `[1,9]`, false},
		{"nested array subset", `{"items":[{"id":1,"name":"a"},{"id":2}]}`, `{"items":[{"id":1}]}`, true},
		{"empty expected matches anything", `{"a":1}`, ``, true},
		{"empty object matches any object", `{"a":1,"b":2}`, `{}`, true},
		{"non-json contains", `hello world foo`, `world`, true},
		{"non-json no match", `hello world`, `xyz`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := JSONSubsetMatch([]byte(tt.actual), []byte(tt.expected))
			if got != tt.want {
				t.Errorf("JSONSubsetMatch(%s, %s) = %v, want %v", tt.actual, tt.expected, got, tt.want)
			}
		})
	}
}

func TestValidateUniqueExpectedRequests(t *testing.T) {
	t.Parallel()
	t.Run("duplicate json same api fails", func(t *testing.T) {
		t.Parallel()
		suite := &Suite{
			Tests: map[string]*Test{
				"test_a": {APIs: map[string]APIConfig{
					"stripe": {Mode: "mock", URL: "/v1", ExpectedRequest: &PayloadSpec{Payload: []byte(`{"x":1}`)}},
				}},
				"test_b": {APIs: map[string]APIConfig{
					"stripe": {Mode: "mock", URL: "/v1", ExpectedRequest: &PayloadSpec{Payload: []byte(`{"x":1}`)}},
				}},
			},
		}
		if err := ValidateUniqueExpectedRequests(suite); err == nil {
			t.Fatal("expected error for duplicate normalized expectation")
		}
	})
	t.Run("same payload different apis ok", func(t *testing.T) {
		t.Parallel()
		payload := []byte(`{"x":1}`)
		suite := &Suite{
			Tests: map[string]*Test{
				"test_a": {APIs: map[string]APIConfig{
					"a": {Mode: "mock", URL: "/a", ExpectedRequest: &PayloadSpec{Payload: payload}},
				}},
				"test_b": {APIs: map[string]APIConfig{
					"b": {Mode: "mock", URL: "/b", ExpectedRequest: &PayloadSpec{Payload: payload}},
				}},
			},
		}
		if err := ValidateUniqueExpectedRequests(suite); err != nil {
			t.Fatal(err)
		}
	})
}
