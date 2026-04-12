// Package engine provides the core Dojo test orchestration logic.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dojo/internal/proxy"
	"dojo/internal/workspace"
)

// EngineOption configures optional Engine behavior.
type EngineOption func(*Engine)

// WithLogger sets the structured logger for the Engine.
func WithLogger(l *slog.Logger) EngineOption {
	return func(e *Engine) {
		e.log = l
	}
}

// WithVerbose enables verbose SUT output forwarding.
func WithVerbose(v bool) EngineOption {
	return func(e *Engine) {
		e.verbose = v
	}
}

// WithTrace enables tracing of HTTP and Postgres payloads.
func WithTrace(t bool) EngineOption {
	return func(e *Engine) {
		e.trace = t
	}
}

// StartupPhaseReport describes the outcome of an optional startup.plan phase
// for console or summary reporting. When Ran is false, DurationMs is zero.
type StartupPhaseReport struct {
	Ran        bool
	DurationMs int64
}

// Engine encapsulates the core Dojo logic for running a Suite.
type Engine struct {
	Workspace     *workspace.Workspace
	Registry      *Registry
	HTTPProxy     *proxy.HTTPProxy
	PostgresProxy *proxy.PostgresProxy
	ActiveSuite   *workspace.Suite
	sutCancel     context.CancelFunc
	log           *slog.Logger
	verbose       bool
	trace         bool

	sutDead     atomic.Bool
	sutDeadCh   chan struct{} // closed when SUT crashes; used to unblock waiting tests
	sutDeadOnce sync.Once     // prevents double-close panic on sutDeadCh
	sutDoneCh   chan struct{} // closed when SUT goroutine returns (normal or crash)
	sutErr      atomic.Value  // stores error
	sutOutput   atomic.Value  // stores string

	// Serializes outbound request correlation so two concurrent calls cannot both
	// match the same ordered expectation index before MarkFulfilled runs.
	processRequestMu sync.Mutex
}

// SUTError returns the SUT crash error if the process exited unexpectedly.
func (e *Engine) SUTError() error {
	v := e.sutErr.Load()
	if v == nil {
		return nil
	}
	return v.(error)
}

// NewEngine initializes a new engine targeting a loaded Workspace.
func NewEngine(ws *workspace.Workspace, opts ...EngineOption) *Engine {
	e := &Engine{
		Workspace:     ws,
		Registry:      NewRegistry(),
		HTTPProxy:     proxy.NewHTTPProxy(),
		PostgresProxy: proxy.NewPostgresProxy(""),
		sutDeadCh:     make(chan struct{}),
	}
	for _, opt := range opts {
		opt(e)
	}
	if e.log == nil {
		e.log = slog.Default()
	}
	e.HTTPProxy.SetLogger(e.log)
	e.PostgresProxy.SetLogger(e.log)
	e.HTTPProxy.Trace = e.trace
	e.PostgresProxy.Trace = e.trace
	return e
}

// StartProxies boots all global/suite-level interceptors.
// When the suite has a startup.plan, the returned report has Ran set and
// DurationMs set to the wall time spent waiting for startup expectations.
func (e *Engine) StartProxies(ctx context.Context, suiteName string) (StartupPhaseReport, error) {
	suite, ok := e.Workspace.Suites[suiteName]
	if !ok {
		return StartupPhaseReport{}, fmt.Errorf("suite '%s' not found", suiteName)
	}
	suite.Config.Timeouts.ResolveDefaults()
	e.ActiveSuite = suite

	var startupReport StartupPhaseReport

	hasPostgres := false
	livePostgres := false
	for _, api := range suite.APIs {
		if api.Protocol == "postgres" || strings.HasPrefix(api.URL, "postgres://") {
			hasPostgres = true
			if api.Mode == "live" {
				livePostgres = true
				e.PostgresProxy.LiveURL = api.URL
			}
		}
	}

	if err := e.checkSeedRequiresLiveDB(filepath.Join(e.Workspace.BaseDir, suiteName, "seed"), livePostgres); err != nil {
		return StartupPhaseReport{}, err
	}
	for testID := range suite.Tests {
		if err := e.checkSeedRequiresLiveDB(filepath.Join(e.Workspace.BaseDir, suiteName, testID, "seed"), livePostgres); err != nil {
			return StartupPhaseReport{}, err
		}
	}

	if suite.Config.Timeouts.Perform.Duration > 0 {
		e.HTTPProxy.UpstreamTimeout = suite.Config.Timeouts.Perform.Duration
	}
	if err := e.HTTPProxy.Start(ctx, "127.0.0.1:0", e); err != nil {
		return StartupPhaseReport{}, fmt.Errorf("failed to start HTTP Proxy: %w", err)
	}

	if hasPostgres {
		if livePostgres && strings.TrimSpace(e.PostgresProxy.LiveURL) != "" {
			e.PostgresProxy.DialAddr = proxy.ExtractPostgresDialAddr(e.PostgresProxy.LiveURL)
		} else {
			e.PostgresProxy.DialAddr = ""
		}

		if err := e.PostgresProxy.Start(ctx, "127.0.0.1:0", e); err != nil {
			return StartupPhaseReport{}, fmt.Errorf("failed to start Postgres Proxy: %w", err)
		}

		if livePostgres {
			if err := e.runSeeds(e.PostgresProxy.LiveURL, filepath.Join(e.Workspace.BaseDir, suiteName, "seed")); err != nil {
				return StartupPhaseReport{}, fmt.Errorf("suite seeding failed: %w", err)
			}
		}
	}

	// Publish API_*_URL env vars so mock response bodies can use $API_*_URL.
	for apiName, apiConfig := range suite.APIs {
		var val string
		if apiConfig.Protocol == "postgres" || strings.HasPrefix(apiConfig.URL, "postgres://") {
			query := "sslmode=disable"
			if u, err := url.Parse(apiConfig.URL); err == nil && u.RawQuery != "" {
				query = u.RawQuery
			}
			val = fmt.Sprintf("postgres://postgres:postgres@%s/postgres?%s", e.PostgresProxy.Addr(), query)
		} else {
			val = fmt.Sprintf("http://%s/%s", e.HTTPProxy.Addr(), apiName)
		}
		os.Setenv(fmt.Sprintf("API_%s_URL", strings.ToUpper(apiName)), val)
	}

	if suite.Config.SutCommand != "" {
		sutCtx, cancel := context.WithCancel(ctx)
		e.sutCancel = cancel

		var startupTest *ActiveTest
		if suite.StartupPlan != "" {
			e.log.Debug("startup plan: preparing expectations",
				"suite", suiteName,
				"plan_bytes", len(suite.StartupPlan),
			)
			st, err := e.prepareStartupPlan(ctx, suite, suiteName)
			if err != nil {
				e.sutCancel()
				return StartupPhaseReport{}, fmt.Errorf("failed to prepare startup plan: %w", err)
			}
			startupTest = st
			n := 0
			for _, exps := range startupTest.Expectations {
				n += len(exps)
			}
			e.log.Debug("startup plan: registered; starting SUT, then waiting for outbound traffic",
				"suite", suiteName,
				"expectations", n,
			)
			e.Registry.Register("startup", startupTest)
		}

		suiteDir := filepath.Join(e.Workspace.BaseDir, suiteName)
		runner := NewSUTRunner(suite.Config.SutCommand, suiteDir)
		runner.suiteDir = suiteDir
		runner.ShutdownGrace = suite.Config.Timeouts.SUTShutdown.Duration
		runner.Verbose = e.verbose
		env := os.Environ()
		runner.Env = env
		e.sutDoneCh = make(chan struct{})

		go func() {
			defer close(e.sutDoneCh)
			e.log.Debug("starting SUT")
			res, err := runner.Run(sutCtx)
			if res.Output != "" {
				e.sutOutput.Store(res.Output)
			}
			if err != nil && sutCtx.Err() == nil {
				e.sutDead.Store(true)
				e.sutErr.Store(err)
				e.sutDeadOnce.Do(func() { close(e.sutDeadCh) })
				e.log.Error("SUT exited unexpectedly", "error", err)
			}
		}()

		waitCtx, waitCancel := context.WithTimeout(ctx, suite.Config.Timeouts.SUTStartup.Duration)
		defer waitCancel()

		if tcpAddr := inferSUTListenTCPAddr(suite); tcpAddr != "" {
			if err := pollTCPDialReady(waitCtx, tcpAddr, suite.Config.Timeouts.TCPPollInterval.Duration, suite.Config.Timeouts.TCPDialTimeout.Duration); err != nil {
				e.sutCancel()
				return StartupPhaseReport{}, fmt.Errorf("waiting for SUT TCP listener on %s: %w", tcpAddr, err)
			}
		}

		if startupTest != nil {
			e.log.Debug("startup plan: SUT is up; awaiting expectations (same timeouts as test Expect phase)",
				"suite", suiteName,
			)
			t0 := time.Now()
			if err := e.awaitPhaseExpectations(ctx, startupTest); err != nil {
				e.log.Error("startup plan failed", "suite", suiteName, "error", err)
				e.Registry.Unregister("startup")
				e.sutCancel()
				return StartupPhaseReport{}, fmt.Errorf("startup plan failed: %w", err)
			}
			startupReport = StartupPhaseReport{
				Ran:        true,
				DurationMs: time.Since(t0).Milliseconds(),
			}
			e.Registry.Unregister("startup")
		}
	}

	return startupReport, nil
}

func inferSUTListenTCPAddr(suite *workspace.Suite) string {
	names := make([]string, 0, len(suite.Entrypoints))
	for n := range suite.Entrypoints {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		ep := suite.Entrypoints[name]
		if strings.ToLower(strings.TrimSpace(ep.Type)) != "http" {
			continue
		}
		raw := strings.TrimSpace(ep.URL)
		if raw == "" {
			return "127.0.0.1:8080"
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return "127.0.0.1:8080"
		}
		host := u.Hostname()
		port := u.Port()
		if port == "" {
			if u.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		if host == "" {
			host = "127.0.0.1"
		}
		return net.JoinHostPort(host, port)
	}
	return ""
}

func pollTCPDialReady(ctx context.Context, addr string, pollInterval, dialTimeout time.Duration) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var d net.Dialer
	for {
		dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		conn, err := d.DialContext(dialCtx, "tcp", addr)
		cancel()
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("waiting for tcp %q: %w", addr, ctx.Err())
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for tcp %q: %w", addr, ctx.Err())
		case <-ticker.C:
		}
	}
}

// StopProxies gracefully tears down any running proxies and the attached SUT.
// It signals the SUT to stop and waits for it to exit before tearing down
// proxies, so the SUT can close its connections cleanly.
func (e *Engine) StopProxies() error {
	if e.sutCancel != nil {
		e.sutCancel()
	}
	if e.sutDoneCh != nil {
		<-e.sutDoneCh
	}
	var pgErr error
	if e.PostgresProxy.Addr() != "" {
		pgErr = e.PostgresProxy.Stop()
	}
	httpErr := e.HTTPProxy.Stop()
	return errors.Join(pgErr, httpErr)
}

// RunSuite executes all tests in a Suite concurrently. The optional onResult
// callback is invoked (from arbitrary goroutines) as each test completes,
// enabling streaming output in the CLI.
func (e *Engine) RunSuite(ctx context.Context, suiteName string, onResult func(workspace.TestResult)) (workspace.TestSummary, error) {
	suite := e.ActiveSuite
	if suite == nil {
		return workspace.TestSummary{}, fmt.Errorf("engine not initialized with a suite")
	}

	suiteStart := time.Now()
	runner := NewRunner(suite.Config.Concurrency)
	summary := workspace.TestSummary{
		TotalRuns: len(suite.Tests),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	for testID, test := range suite.Tests {
		wg.Add(1)
		runner.Submit(func(id string, t *workspace.Test) func() {
			return func() {
				defer wg.Done()

				if e.sutDead.Load() {
					mu.Lock()
					summary.Failed++
					reason := "SUT process exited unexpectedly"
					if sutErr := e.SUTError(); sutErr != nil {
						reason = sutErr.Error()
					}
					tr := workspace.TestResult{TestName: id, Status: "fail", Reason: reason}
					summary.Results = append(summary.Results, tr)
					summary.Failures = append(summary.Failures, workspace.TestFailure{
						TestName: id, Reason: reason,
					})
					mu.Unlock()
					if onResult != nil {
						onResult(tr)
					}
					return
				}

				start := time.Now()
				usage, err := e.executeTest(ctx, id, t, suite, suiteName)
				dur := time.Since(start)

				mu.Lock()
				defer mu.Unlock()

				tr := workspace.TestResult{
					TestName:   id,
					DurationMs: dur.Milliseconds(),
				}
				if usage.TotalTokens > 0 {
					tr.LLMUsage = &usage
				}

				if err != nil {
					tr.Status = "fail"
					tr.Reason = err.Error()
					failure := workspace.TestFailure{
						TestName:   id,
						Reason:     err.Error(),
						DurationMs: dur.Milliseconds(),
					}
					var mm *MismatchError
					if errors.As(err, &mm) {
						tr.Expected = mm.Expected
						tr.Actual = mm.Actual
						failure.Expected = mm.Expected
						failure.Actual = mm.Actual
					}
					summary.Failed++
					summary.Failures = append(summary.Failures, failure)
				} else {
					tr.Status = "pass"
					summary.Passed++
				}

				summary.Results = append(summary.Results, tr)
				if onResult != nil {
					onResult(tr)
				}
			}
		}(testID, test))
	}

	wg.Wait()
	summary.DurationMs = time.Since(suiteStart).Milliseconds()

	if v := e.sutOutput.Load(); v != nil {
		if s, ok := v.(string); ok && summary.Failed > 0 {
			summary.SutOutput = s
		}
	}

	return summary, nil
}
