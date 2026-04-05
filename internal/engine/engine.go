// Package engine provides the core Dojo test orchestration logic.
package engine

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"dojo/internal/proxy"
	"dojo/internal/workspace"
	"dojo/pkg/dojo"
)

// EngineOption configures optional Engine behavior.
type EngineOption func(*Engine)

// WithLogger sets the structured logger for the Engine.
func WithLogger(l *slog.Logger) EngineOption {
	return func(e *Engine) {
		e.log = l
	}
}

// Engine encapsulates the core Dojo logic for running a Suite.
type Engine struct {
	Workspace     *workspace.Workspace
	Registry      *Registry
	HTTPProxy     *proxy.HTTPProxy
	PostgresProxy *proxy.PostgresProxy
	ActiveSuite   *workspace.Suite
	sutCancel     context.CancelFunc
	Adapters      []dojo.Adapter
	log           *slog.Logger
}

// NewEngine initializes a new engine targeting a loaded Workspace.
func NewEngine(ws *workspace.Workspace, opts ...EngineOption) *Engine {
	e := &Engine{
		Workspace:     ws,
		Registry:      NewRegistry(),
		HTTPProxy:     proxy.NewHTTPProxy(),
		PostgresProxy: proxy.NewPostgresProxy(""),
		Adapters:      []dojo.Adapter{},
	}
	for _, opt := range opts {
		opt(e)
	}
	if e.log == nil {
		e.log = slog.Default()
	}
	e.HTTPProxy.SetLogger(e.log)
	e.PostgresProxy.SetLogger(e.log)
	return e
}

// RegisterAdapter adds a new protocol adapter to the engine.
func (e *Engine) RegisterAdapter(adapter dojo.Adapter) {
	e.Adapters = append(e.Adapters, adapter)
}

// StartProxies boots all global/suite-level interceptors.
func (e *Engine) StartProxies(ctx context.Context, suiteName string) error {
	suite, ok := e.Workspace.Suites[suiteName]
	if !ok {
		return fmt.Errorf("suite '%s' not found", suiteName)
	}
	suite.Config.Timeouts.ResolveDefaults()
	e.ActiveSuite = suite

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
		return err
	}
	for testID := range suite.Tests {
		if err := e.checkSeedRequiresLiveDB(filepath.Join(e.Workspace.BaseDir, suiteName, testID, "seed"), livePostgres); err != nil {
			return err
		}
	}

	if err := e.HTTPProxy.Start(ctx, "127.0.0.1:0", e); err != nil {
		return fmt.Errorf("failed to start HTTP Proxy: %w", err)
	}

	if hasPostgres {
		if livePostgres && strings.TrimSpace(e.PostgresProxy.LiveURL) != "" {
			e.PostgresProxy.DialAddr = proxy.ExtractPostgresDialAddr(e.PostgresProxy.LiveURL)
		} else {
			e.PostgresProxy.DialAddr = ""
		}

		if err := e.PostgresProxy.Start(ctx, "127.0.0.1:0", e); err != nil {
			return fmt.Errorf("failed to start Postgres Proxy: %w", err)
		}

		if livePostgres {
			if err := e.runSeeds(e.PostgresProxy.LiveURL, filepath.Join(e.Workspace.BaseDir, suiteName, "seed")); err != nil {
				return fmt.Errorf("suite seeding failed: %w", err)
			}
		}
	}

	if suite.Config.SutCommand != "" {
		sutCtx, cancel := context.WithCancel(context.Background())
		e.sutCancel = cancel

		runner := NewSUTRunner(suite.Config.SutCommand, filepath.Join(e.Workspace.BaseDir, suiteName))
		env := os.Environ()
		for apiName, apiConfig := range suite.APIs {
			if apiConfig.Protocol == "postgres" || strings.HasPrefix(apiConfig.URL, "postgres://") {
				env = append(env, fmt.Sprintf("API_%s_URL=postgres://postgres:postgres@%s/postgres?sslmode=disable", strings.ToUpper(apiName), e.PostgresProxy.Addr()))
			} else {
				env = append(env, fmt.Sprintf("API_%s_URL=http://%s/%s", strings.ToUpper(apiName), e.HTTPProxy.Addr(), apiName))
			}
		}
		runner.Env = env

		go func() {
			e.log.Info("starting SUT")
			res, err := runner.Run(sutCtx)
			if err != nil && sutCtx.Err() == nil {
				e.log.Error("SUT failed", "error", err, "output", res.Output)
			}
			if res.Output != "" {
				e.log.Debug("SUT output", "output", res.Output)
			}
		}()

		waitCtx, waitCancel := context.WithTimeout(ctx, suite.Config.Timeouts.SUTStartup.Duration)
		defer waitCancel()

		if tcpAddr := inferSUTListenTCPAddr(suite); tcpAddr != "" {
			if err := pollTCPDialReady(waitCtx, tcpAddr, suite.Config.Timeouts.TCPPollInterval.Duration, suite.Config.Timeouts.TCPDialTimeout.Duration); err != nil {
				return fmt.Errorf("waiting for SUT TCP listener on %s: %w", tcpAddr, err)
			}
		}
	}

	return nil
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
func (e *Engine) StopProxies() error {
	if e.sutCancel != nil {
		e.sutCancel()
	}
	if e.PostgresProxy.Addr() != "" {
		e.PostgresProxy.Stop()
	}
	return e.HTTPProxy.Stop()
}

// RunSuite executes all tests in a Suite concurrently.
func (e *Engine) RunSuite(ctx context.Context, suiteName string) (workspace.TestSummary, error) {
	suite := e.ActiveSuite
	if suite == nil {
		return workspace.TestSummary{}, fmt.Errorf("engine not initialized with a suite")
	}

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

				err := e.executeTest(ctx, id, t, suite, suiteName)

				mu.Lock()
				defer mu.Unlock()

				if err != nil {
					summary.Failed++
					summary.Failures = append(summary.Failures, workspace.TestFailure{
						TestName: id,
						Reason:   err.Error(),
					})
				} else {
					summary.Passed++
				}
			}
		}(testID, test))
	}

	wg.Wait()
	return summary, nil
}
