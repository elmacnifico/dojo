// Package workspace provides configuration structures and file parsers for Dojo.
package workspace

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PayloadSpec configures raw payload matching for request or response assertions.
type PayloadSpec struct {
	Body    string `json:"body,omitempty"`
	File    string `json:"file,omitempty"`
	Payload []byte `json:"-"`
}

// DefaultResponse defines the payload returned to the SUT upon successful correlation.
type DefaultResponse struct {
	Body    string `json:"body,omitempty"`
	File    string `json:"file,omitempty"`
	Code    int    `json:"code,omitempty"`
	Payload []byte `json:"-"`
}

// APIConfig controls the mode and URL behavior for an outbound SUT dependency.
type APIConfig struct {
	Protocol         string            `json:"protocol,omitempty"` // "http", "postgres" (defaults to "http")
	Mode             string            `json:"mode,omitempty"`     // "mock" or "live"
	Timeout          string            `json:"timeout"`
	URL              string            `json:"url"`
	Headers          map[string]string `json:"headers,omitempty"` // For API keys via env vars
	ExpectedRequest  *PayloadSpec      `json:"expected_request,omitempty"`
	ExpectedResponse *PayloadSpec      `json:"expected_response,omitempty"`
	DefaultResponse  *DefaultResponse  `json:"default_response,omitempty"`
}

// EntrypointConfig represents how Dojo triggers the SUT to start a test.
type EntrypointConfig struct {
	Type             string            `json:"type"`
	Path             string            `json:"path"`
	URL              string            `json:"url,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	ExpectedResponse *PayloadSpec      `json:"expected_response,omitempty"`
}

// EvaluatorConfig holds the rules for AI evaluation.
type EvaluatorConfig struct {
	Provider  string `json:"provider"`      // "gemini", "openai", "anthropic"
	Model     string `json:"model"`         // e.g., "gemini-1.5-flash", "gpt-4"
	APIKeyEnv string `json:"api_key_env"`   // e.g., "GEMINI_API_KEY"
	URL       string `json:"url,omitempty"` // For custom/local endpoints
}

// Duration wraps time.Duration for JSON marshaling as a Go duration string (e.g. "5s", "300ms").
type Duration struct {
	time.Duration
}

// UnmarshalJSON parses a JSON string like "5s" or "300ms" into a Duration.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"5s\": %w", err)
	}
	if s == "" {
		d.Duration = 0
		return nil
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = parsed
	return nil
}

// MarshalJSON writes the Duration as a Go duration string.
func (d Duration) MarshalJSON() ([]byte, error) {
	if d.Duration == 0 {
		return json.Marshal("")
	}
	return json.Marshal(d.Duration.String())
}

// Default timeout values used when TimeoutConfig fields are zero.
const (
	DefaultSUTStartup      = 90 * time.Second
	DefaultSUTShutdown     = 5 * time.Second
	DefaultTCPPollInterval = 50 * time.Millisecond
	DefaultTCPDialTimeout  = 300 * time.Millisecond
	DefaultHTTPClient      = 5 * time.Second
	DefaultAIEvaluator     = 30 * time.Second
)

// TimeoutConfig holds configurable timeout durations for the engine.
type TimeoutConfig struct {
	SUTStartup      Duration `json:"sut_startup,omitempty"`
	SUTShutdown     Duration `json:"sut_shutdown,omitempty"`
	TCPPollInterval Duration `json:"tcp_poll_interval,omitempty"`
	TCPDialTimeout  Duration `json:"tcp_dial_timeout,omitempty"`
	HTTPClient      Duration `json:"http_client,omitempty"`
	AIEvaluator     Duration `json:"ai_evaluator,omitempty"`
}

// ResolveDefaults fills zero-valued fields with sensible defaults.
func (tc *TimeoutConfig) ResolveDefaults() {
	if tc.SUTStartup.Duration == 0 {
		tc.SUTStartup.Duration = DefaultSUTStartup
	}
	if tc.SUTShutdown.Duration == 0 {
		tc.SUTShutdown.Duration = DefaultSUTShutdown
	}
	if tc.TCPPollInterval.Duration == 0 {
		tc.TCPPollInterval.Duration = DefaultTCPPollInterval
	}
	if tc.TCPDialTimeout.Duration == 0 {
		tc.TCPDialTimeout.Duration = DefaultTCPDialTimeout
	}
	if tc.HTTPClient.Duration == 0 {
		tc.HTTPClient.Duration = DefaultHTTPClient
	}
	if tc.AIEvaluator.Duration == 0 {
		tc.AIEvaluator.Duration = DefaultAIEvaluator
	}
}

// DojoConfig holds suite-level settings.
type DojoConfig struct {
	Concurrency int `json:"concurrency"`
	// SutCommand, when non-empty, starts the SUT as a child process before tests run. The engine
	// then waits until the first HTTP entrypoint's TCP listen address accepts connections (host:port
	// from the entrypoint URL, or 127.0.0.1:8080 when that URL is empty).
	SutCommand string           `json:"sut_command,omitempty"`
	Evaluator  *EvaluatorConfig `json:"evaluator,omitempty"`
	Timeouts   TimeoutConfig    `json:"timeouts,omitempty"`
}

// Test holds a distinct test configuration mapped by its folder.
type Test struct {
	APIs map[string]APIConfig
	Plan string
	Eval string
}

// Suite is a collection of tests with unified global APIs and configuration.
type Suite struct {
	Config      DojoConfig
	APIs        map[string]APIConfig
	Entrypoints map[string]EntrypointConfig
	Tests       map[string]*Test
	Eval        string
}

// Workspace encapsulates all discovered suites and global execution properties.
type Workspace struct {
	BaseDir    string
	Suites     map[string]*Suite
	GlobalEval string
}

// TestResult captures the outcome of a single test execution.
type TestResult struct {
	TestName   string        `json:"test_name"`
	Status     string        `json:"status"` // "pass" or "fail"
	DurationMs int64         `json:"duration_ms"`
	Reason     string        `json:"reason,omitempty"`
	Expected   string        `json:"expected,omitempty"`
	Actual     string        `json:"actual,omitempty"`
}

// TestFailure captures a failed assertion during test execution.
type TestFailure struct {
	TestName   string `json:"test_name"`
	Expected   string `json:"expected"`
	Actual     string `json:"actual"`
	Diff       string `json:"diff"`
	Reason     string `json:"reason"`
	DurationMs int64  `json:"duration_ms,omitempty"`
}

// TestSummary encapsulates the overall results of a suite execution.
type TestSummary struct {
	TotalRuns  int           `json:"total_runs"`
	Passed     int           `json:"passed"`
	Failed     int           `json:"failed"`
	DurationMs int64         `json:"duration_ms,omitempty"`
	SutOutput  string        `json:"sut_output,omitempty"`
	Failures   []TestFailure `json:"failures"`
	Results    []TestResult  `json:"results,omitempty"`
}

// LoadWorkspace recursively discovers all test suites and configurations.
func LoadWorkspace(baseDir string) (*Workspace, error) {
	ws := &Workspace{
		BaseDir: baseDir,
		Suites:  make(map[string]*Suite),
	}

	// Read Global Eval
	if b, err := os.ReadFile(filepath.Join(baseDir, "eval.md")); err == nil {
		ws.GlobalEval = strings.TrimSpace(string(b))
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, fmt.Errorf("reading workspace directory %s: %w", baseDir, err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		suitePath := filepath.Join(baseDir, e.Name())
		configPath := filepath.Join(suitePath, "dojo.config")

		if _, err := os.Stat(configPath); err == nil {
			suite := &Suite{
				APIs:        make(map[string]APIConfig),
				Entrypoints: make(map[string]EntrypointConfig),
				Tests:       make(map[string]*Test),
			}
			if err := loadJSON(configPath, &suite.Config); err != nil {
				return nil, err
			}
			if err := validateSuiteConfig(e.Name(), &suite.Config); err != nil {
				return nil, err
			}

			// Read Suite APIs
			apisDir := filepath.Join(suitePath, "apis")
			if apiEntries, err := os.ReadDir(apisDir); err == nil {
				for _, apiE := range apiEntries {
					if strings.HasSuffix(apiE.Name(), ".json") {
						name := strings.TrimSuffix(apiE.Name(), ".json")
						var cfg APIConfig
						if err := loadJSON(filepath.Join(apisDir, apiE.Name()), &cfg); err != nil {
							return nil, err
						}
						expandAPIConfig(&cfg)
						if err := validateAPIConfig(name, &cfg); err != nil {
							return nil, err
						}
						if err := resolvePayload(&cfg, suitePath, ""); err != nil {
							return nil, err
						}
						suite.APIs[name] = cfg
					}
				}
			}

			// Read Suite Entrypoints
			entrypointsDir := filepath.Join(suitePath, "entrypoints")
			if epEntries, err := os.ReadDir(entrypointsDir); err == nil {
				for _, epE := range epEntries {
					if strings.HasSuffix(epE.Name(), ".json") {
						name := strings.TrimSuffix(epE.Name(), ".json")
						var cfg EntrypointConfig
						if err := loadJSON(filepath.Join(entrypointsDir, epE.Name()), &cfg); err != nil {
							return nil, err
						}
						expandEntrypointConfig(&cfg)
						if err := validateEntrypointConfig(name, &cfg); err != nil {
							return nil, err
						}

						// Load expected response fixture if provided
						if cfg.ExpectedResponse != nil {
							if cfg.ExpectedResponse.File != "" {
								b, err := os.ReadFile(filepath.Join(suitePath, cfg.ExpectedResponse.File))
								if err != nil {
									return nil, fmt.Errorf("failed to read entrypoint expected response %s: %w", cfg.ExpectedResponse.File, err)
								}
								cfg.ExpectedResponse.Payload = b
							} else if cfg.ExpectedResponse.Body != "" {
								cfg.ExpectedResponse.Payload = []byte(cfg.ExpectedResponse.Body)
							}
						}

						suite.Entrypoints[name] = cfg
					}
				}
			}

			// Read Suite Eval
			if b, err := os.ReadFile(filepath.Join(suitePath, "eval.md")); err == nil {
				suite.Eval = strings.TrimSpace(string(b))
			}

			// Read Tests
			testEntries, err := os.ReadDir(suitePath)
			if err != nil {
				return nil, fmt.Errorf("reading suite directory %s: %w", suitePath, err)
			}
			for _, te := range testEntries {
				if te.IsDir() && strings.HasPrefix(te.Name(), "test_") {
						testPath := filepath.Join(suitePath, te.Name())
						test := &Test{
							APIs: make(map[string]APIConfig),
						}

						// Read Test APIs overrides
						testAPIsDir := filepath.Join(testPath, "apis")
						if tapEntries, err := os.ReadDir(testAPIsDir); err == nil {
							for _, tae := range tapEntries {
								if strings.HasSuffix(tae.Name(), ".json") {
									name := strings.TrimSuffix(tae.Name(), ".json")
									var cfg APIConfig
									if suiteCfg, ok := suite.APIs[name]; ok {
										cfg = CopyAPIConfig(suiteCfg)
									}
									if err := loadJSON(filepath.Join(testAPIsDir, tae.Name()), &cfg); err != nil {
										return nil, err
									}
									expandAPIConfig(&cfg)
									if err := validateAPIConfig(name, &cfg); err != nil {
										return nil, fmt.Errorf("in test %s: %w", te.Name(), err)
									}
									test.APIs[name] = cfg
								}
							}
						}

						// Read Test Plan
						planFiles, err := filepath.Glob(filepath.Join(testPath, "*.plan"))
						if err != nil || len(planFiles) == 0 {
							return nil, fmt.Errorf("missing .plan file in %s", testPath)
						}
						if len(planFiles) > 1 {
							return nil, fmt.Errorf("multiple .plan files found in %s, please provide only one", testPath)
						}

						planPath := planFiles[0]
						planName := filepath.Base(planPath)

						planBytes, err := os.ReadFile(planPath)
						if err != nil {
							return nil, fmt.Errorf("failed to read plan file %s: %w", planPath, err)
						}
						planStr := string(planBytes)
						parsedPlan, err := ParsePlan(planStr)
						if err != nil {
							return nil, fmt.Errorf("failed to parse %s in %s: %w", planName, testPath, err)
						}
						if len(parsedPlan.Lines) == 0 {
							return nil, fmt.Errorf("%s in %s is empty", planName, testPath)
						}
						if strings.ToLower(parsedPlan.Lines[0].Action) != "perform" {
							return nil, fmt.Errorf("%s in %s must start with 'Perform'", planName, testPath)
						}
						test.Plan = planStr

						// Wire fixtures from plan clauses into API configs.
						if err := wireFixturesFromPlan(parsedPlan, test, suite, testPath, suitePath); err != nil {
							return nil, fmt.Errorf("in test %s: %w", te.Name(), err)
						}

					inheritedEval := suite.Eval
					if inheritedEval == "" {
						inheritedEval = ws.GlobalEval
					}

					evalPath := filepath.Join(testPath, "eval.md")
					if b, err := os.ReadFile(evalPath); err == nil {
						content := strings.TrimSpace(string(b))
						if strings.HasPrefix(content, "+") {
							test.Eval = inheritedEval + "\n" + strings.TrimSpace(strings.TrimPrefix(content, "+"))
						} else {
							test.Eval = content
						}
					} else {
						test.Eval = inheritedEval
					}

						suite.Tests[te.Name()] = test
				}
			}

			if err := ValidateUniqueExpectedRequests(suite); err != nil {
				return nil, fmt.Errorf("suite %s: %w", e.Name(), err)
			}

			ws.Suites[e.Name()] = suite
		}
	}

	return ws, nil
}

// CopyAPIConfig returns a deep copy of an APIConfig, cloning all pointer fields,
// the Headers map, and Payload byte slices so mutations to the copy never
// affect the original.
func CopyAPIConfig(src APIConfig) APIConfig {
	dst := src
	if src.Headers != nil {
		dst.Headers = make(map[string]string, len(src.Headers))
		for k, v := range src.Headers {
			dst.Headers[k] = v
		}
	}
	if src.ExpectedRequest != nil {
		e := *src.ExpectedRequest
		e.Payload = cloneBytes(src.ExpectedRequest.Payload)
		dst.ExpectedRequest = &e
	}
	if src.ExpectedResponse != nil {
		e := *src.ExpectedResponse
		e.Payload = cloneBytes(src.ExpectedResponse.Payload)
		dst.ExpectedResponse = &e
	}
	if src.DefaultResponse != nil {
		r := *src.DefaultResponse
		r.Payload = cloneBytes(src.DefaultResponse.Payload)
		dst.DefaultResponse = &r
	}
	return dst
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

// wireFixturesFromPlan reads Request and Respond clauses from the parsed plan
// and wires them into the test's [APIConfig] entries. For each Expect line, the
// API config is copied from the suite when the test does not already have an
// override. Fixture payloads are resolved via [resolvePayload] (test dir first,
// suite dir fallback, deep merge for JSON).
func wireFixturesFromPlan(doc *ParsedDocument, test *Test, suite *Suite, testPath, suitePath string) error {
	for _, line := range doc.Lines {
		if strings.ToLower(line.Action) != "expect" {
			continue
		}
		apiName := line.Target

		if _, ok := test.APIs[apiName]; !ok {
			if suiteCfg, ok := suite.APIs[apiName]; ok {
				test.APIs[apiName] = CopyAPIConfig(suiteCfg)
			} else {
				return fmt.Errorf("API %q referenced in plan but not defined in apis/", apiName)
			}
		}
		cfg := test.APIs[apiName]

		for _, clause := range line.Clauses {
			if clause.Value == nil {
				continue
			}
			switch strings.ToLower(clause.Key) {
			case "request":
				cfg.ExpectedRequest = &PayloadSpec{File: *clause.Value}
			case "respond":
				code := 200
				if cfg.DefaultResponse != nil {
					code = cfg.DefaultResponse.Code
				}
				cfg.DefaultResponse = &DefaultResponse{File: *clause.Value, Code: code}
			}
		}

		if err := resolvePayload(&cfg, testPath, suitePath); err != nil {
			return fmt.Errorf("API %s: %w", apiName, err)
		}
		test.APIs[apiName] = cfg
	}
	return nil
}

func loadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	return nil
}

// validateEntrypointConfig checks that an entrypoint has a valid type. Unknown
// types fail at load time with a clear message instead of at test execution time.
func validateEntrypointConfig(name string, cfg *EntrypointConfig) error {
	t := strings.ToLower(strings.TrimSpace(cfg.Type))
	if t == "" {
		return fmt.Errorf("entrypoint %s: type must not be empty", name)
	}
	switch t {
	case "http":
	default:
		return fmt.Errorf("entrypoint %s: unknown type %q (supported: http)", name, cfg.Type)
	}
	return nil
}

// validateSuiteConfig checks suite-level config for invalid values that would
// cause panics or confusing runtime errors. Called at load time so problems
// surface before any test execution begins.
func validateSuiteConfig(suiteName string, cfg *DojoConfig) error {
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}

	if eval := cfg.Evaluator; eval != nil {
		switch strings.ToLower(eval.Provider) {
		case "gemini", "openai", "anthropic":
		default:
			return fmt.Errorf("suite %s: evaluator provider must be one of gemini, openai, anthropic; got %q", suiteName, eval.Provider)
		}
		if eval.Model == "" {
			return fmt.Errorf("suite %s: evaluator model must not be empty", suiteName)
		}
		if eval.APIKeyEnv == "" {
			return fmt.Errorf("suite %s: evaluator api_key_env must not be empty", suiteName)
		}
	}

	tc := cfg.Timeouts
	durations := []struct {
		name string
		val  Duration
	}{
		{"sut_startup", tc.SUTStartup},
		{"sut_shutdown", tc.SUTShutdown},
		{"tcp_poll_interval", tc.TCPPollInterval},
		{"tcp_dial_timeout", tc.TCPDialTimeout},
		{"http_client", tc.HTTPClient},
		{"ai_evaluator", tc.AIEvaluator},
	}
	for _, d := range durations {
		if d.val.Duration < 0 {
			return fmt.Errorf("suite %s: timeout %s must not be negative, got %s", suiteName, d.name, d.val.Duration)
		}
	}

	return nil
}

func validateAPIConfig(name string, cfg *APIConfig) error {
	if cfg.Protocol == "" {
		cfg.Protocol = "http"
	}
	if cfg.Mode == "live" {
		if cfg.URL == "" {
			return fmt.Errorf("API %s is live but has no URL", name)
		}
		u, err := url.Parse(cfg.URL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("API %s is live but has invalid URL (must have scheme and host): %s", name, cfg.URL)
		}
	} else if cfg.Mode == "mock" {
		if cfg.URL != "" {
			u, err := url.Parse(cfg.URL)
			if err == nil && u.Host != "" {
				return fmt.Errorf("API %s is mock but URL contains a domain: %s", name, cfg.URL)
			}
		}
	} else {
		return fmt.Errorf("API %s must have mode 'live' or 'mock', got '%s'", name, cfg.Mode)
	}
	return nil
}

func expandAPIConfig(cfg *APIConfig) {
	cfg.URL = os.ExpandEnv(cfg.URL)
	if cfg.Headers != nil {
		for k, v := range cfg.Headers {
			cfg.Headers[k] = os.ExpandEnv(v)
		}
	}
}

func expandEntrypointConfig(cfg *EntrypointConfig) {
	cfg.URL = os.ExpandEnv(cfg.URL)
	cfg.Path = os.ExpandEnv(cfg.Path)
	if cfg.Headers != nil {
		for k, v := range cfg.Headers {
			cfg.Headers[k] = os.ExpandEnv(v)
		}
	}
}

// resolveFile reads a file from primaryDir, falling back to fallbackDir. When
// the same filename exists in both directories and both are valid JSON objects,
// the fallback (suite) acts as the base and the primary (test) is deep-merged
// on top, so test fixtures only need to carry the fields that differ.
func resolveFile(filename, primaryDir, fallbackDir string) ([]byte, error) {
	primary, primaryErr := os.ReadFile(filepath.Join(primaryDir, filename))
	var fallback []byte
	var fallbackErr error
	if fallbackDir != "" {
		fallback, fallbackErr = os.ReadFile(filepath.Join(fallbackDir, filename))
	} else {
		fallbackErr = fmt.Errorf("no fallback dir")
	}

	switch {
	case primaryErr != nil && fallbackErr != nil:
		return nil, fmt.Errorf("resolve payload file %s: %w", filename, primaryErr)
	case primaryErr != nil:
		return fallback, nil
	case fallbackErr != nil:
		return primary, nil
	default:
		return deepMergeJSON(fallback, primary)
	}
}

// deepMergeJSON merges two JSON byte slices, treating base as the default and
// overlay as the override. Only JSON objects are merged recursively; if either
// input is not a JSON object the overlay is returned as-is.
func deepMergeJSON(base, overlay []byte) ([]byte, error) {
	var baseMap, overlayMap map[string]any
	if err := json.Unmarshal(base, &baseMap); err != nil {
		return overlay, nil
	}
	if err := json.Unmarshal(overlay, &overlayMap); err != nil {
		return overlay, nil
	}
	merged := mergeMaps(baseMap, overlayMap)
	return json.Marshal(merged)
}

// mergeMaps recursively merges overlay into base. Nested maps are merged;
// arrays and scalar values in overlay replace those in base.
func mergeMaps(base, overlay map[string]any) map[string]any {
	result := make(map[string]any, len(base))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range overlay {
		if baseVal, exists := result[k]; exists {
			baseMap, baseOK := baseVal.(map[string]any)
			overlayMap, overlayOK := v.(map[string]any)
			if baseOK && overlayOK {
				result[k] = mergeMaps(baseMap, overlayMap)
				continue
			}
		}
		result[k] = v
	}
	return result
}

func resolvePayload(cfg *APIConfig, primaryDir string, fallbackDir string) error {
	if cfg.ExpectedRequest != nil {
		if cfg.ExpectedRequest.File != "" {
			b, err := resolveFile(cfg.ExpectedRequest.File, primaryDir, fallbackDir)
			if err != nil {
				return fmt.Errorf("expected_request: %w", err)
			}
			cfg.ExpectedRequest.Payload = b
		} else if cfg.ExpectedRequest.Body != "" {
			cfg.ExpectedRequest.Payload = []byte(cfg.ExpectedRequest.Body)
		}
	}

	if cfg.ExpectedResponse != nil {
		if cfg.ExpectedResponse.File != "" {
			b, err := resolveFile(cfg.ExpectedResponse.File, primaryDir, fallbackDir)
			if err != nil {
				return fmt.Errorf("expected_response: %w", err)
			}
			cfg.ExpectedResponse.Payload = b
		} else if cfg.ExpectedResponse.Body != "" {
			cfg.ExpectedResponse.Payload = []byte(cfg.ExpectedResponse.Body)
		}
	}

	if cfg.DefaultResponse != nil {
		if cfg.DefaultResponse.File != "" {
			b, err := resolveFile(cfg.DefaultResponse.File, primaryDir, fallbackDir)
			if err != nil {
				return fmt.Errorf("default_response: %w", err)
			}
			cfg.DefaultResponse.Payload = b
		} else if cfg.DefaultResponse.Body != "" {
			cfg.DefaultResponse.Payload = []byte(cfg.DefaultResponse.Body)
		}
	}
	return nil
}
