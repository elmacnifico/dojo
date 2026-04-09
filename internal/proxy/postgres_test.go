package proxy_test

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"dojo/internal/engine"
	"dojo/internal/testutil"
	"dojo/internal/workspace"
)

func TestPostgresQueryCapture(t *testing.T) {
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("docker not available (required for testcontainers): %v\n%s", err, out)
	}

	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("postgres"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(5*time.Second)),
	)
	if err != nil {
		t.Fatalf("Failed to start Postgres container: %v", err)
	}
	defer func() {
		if err := pgContainer.Terminate(ctx); err != nil {
			t.Fatalf("Failed to terminate Postgres container: %v", err)
		}
	}()

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	var onTrigger func()
	sutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/trigger" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.ReadAll(r.Body)
		if onTrigger != nil {
			onTrigger()
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok"}`))
	}))
	defer sutServer.Close()

	tmpDir := t.TempDir()
	testutil.CreateFile(t, tmpDir, "test_suite/dojo.config", `{"concurrency": 1}`)
	testutil.CreateFile(t, tmpDir, "test_suite/apis/postgres.json", `{"mode": "live", "protocol": "postgres", "url": "`+connStr+`"}`)
	testutil.CreateFile(t, tmpDir, "test_suite/entrypoints/webhook.json", `{"type": "http", "path": "/trigger", "url": "`+sutServer.URL+`"}`)

	testutil.CreateFile(t, tmpDir, "test_suite/test_001/test.plan", `
Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> postgres -> Payload: ""
`)
	testutil.CreateFile(t, tmpDir, "test_suite/test_001/incoming.json", `{"id": "test_001"}`)
	testutil.CreateFile(t, tmpDir, "test_suite/test_001/apis/postgres.json", `{
		"expected_request": {"body": "INSERT INTO users (name) VALUES ('test_001')"},
		"expected_response": {"body": "INSERT 0 1"}
	}`)

	testutil.CreateFile(t, tmpDir, "test_suite/seed/schema.sql", "CREATE TABLE IF NOT EXISTS users (id SERIAL PRIMARY KEY, name TEXT);")

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load workspace: %v", err)
	}

	eng := engine.NewEngine(ws)


	if err := eng.StartProxies(ctx, "test_suite"); err != nil {
		t.Fatalf("Failed to start proxies: %v", err)
	}
	defer eng.StopProxies()

	pgProxyURL := "postgres://postgres:postgres@" + eng.PostgresProxy.Addr() + "/postgres?sslmode=disable"
	db, err := sql.Open("postgres", pgProxyURL)
	if err != nil {
		t.Fatalf("Failed to connect via proxy: %v", err)
	}
	defer db.Close()

	onTrigger = func() {
		if _, err := db.ExecContext(ctx, "INSERT INTO users (name) VALUES ('test_001')"); err != nil {
			t.Errorf("insert via proxy: %v", err)
		}
	}

	suiteCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	summary, err := eng.RunSuite(suiteCtx, "test_suite", nil)
	if err != nil {
		t.Fatalf("RunSuite failed: %v", err)
	}

	if summary.Failed > 0 {
		t.Errorf("Expected 0 failures, got %d. Failures: %v", summary.Failed, summary.Failures)
	}

	tmpDir2 := t.TempDir()
	testutil.CreateFile(t, tmpDir2, "test_suite/dojo.config", `{"concurrency": 1}`)
	testutil.CreateFile(t, tmpDir2, "test_suite/apis/postgres.json", `{"mode": "mock", "protocol": "postgres", "url": ""}`)
	testutil.CreateFile(t, tmpDir2, "test_suite/entrypoints/webhook.json", `{"type": "http", "path": "/trigger", "url": "`+sutServer.URL+`"}`)

	testutil.CreateFile(t, tmpDir2, "test_suite/test_002/test.plan", `
Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> postgres -> Payload: ""
`)
	testutil.CreateFile(t, tmpDir2, "test_suite/test_002/incoming.json", `{"id": "test_002"}`)
	testutil.CreateFile(t, tmpDir2, "test_suite/test_002/apis/postgres.json", `{
		"mode": "mock",
		"protocol": "postgres",
		"url": "",
		"expected_request": {"body": "INSERT INTO users (name) VALUES ('test_002')"}
	}`)

	ws2, err := workspace.LoadWorkspace(tmpDir2)
	if err != nil {
		t.Fatalf("Failed to load workspace 2: %v", err)
	}
	eng2 := engine.NewEngine(ws2)


	if err := eng2.StartProxies(ctx, "test_suite"); err != nil {
		t.Fatalf("Failed to start proxies 2: %v", err)
	}
	defer eng2.StopProxies()

	pgProxyURL2 := "postgres://postgres:postgres@" + eng2.PostgresProxy.Addr() + "/postgres?sslmode=disable"
	db2, err := sql.Open("postgres", pgProxyURL2)
	if err != nil {
		t.Fatalf("open db2: %v", err)
	}
	defer db2.Close()

	onTrigger = func() {
		if _, err := db2.ExecContext(ctx, "INSERT INTO users (name) VALUES ('test_002')"); err != nil {
			t.Errorf("insert mock mode: %v", err)
		}
	}

	suiteCtx2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()

	summary2, err := eng2.RunSuite(suiteCtx2, "test_suite", nil)
	if err != nil {
		t.Fatalf("RunSuite 2: %v", err)
	}
	if summary2.Failed > 0 {
		t.Errorf("Expected 0 failures in mock mode, got %d: %v", summary2.Failed, summary2.Failures)
	}
}
