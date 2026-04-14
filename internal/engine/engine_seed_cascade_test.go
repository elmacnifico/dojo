package engine_test

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/elmacnifico/dojo/internal/engine"
	"github.com/elmacnifico/dojo/internal/testutil"
	"github.com/elmacnifico/dojo/internal/workspace"
)

// TestSeedFailureCascadesToWholeSuite ensures one broken per-test seed marks
// every test failed after [engine.Engine.RunSuite] reconciliation.
func TestSeedFailureCascadesToWholeSuite(t *testing.T) {
	if _, err := os.Stat("/var/run/docker.sock"); err != nil {
		t.Skip("docker not available (required for testcontainers)")
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
		t.Fatalf("postgres: %v", err)
	}
	defer func() {
		_ = pgContainer.Terminate(ctx)
	}()

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	// db is set after StartProxies (proxy address). Handler mirrors postgres_test:
	// issue the INSERT the test expects so the postgres mock can match.
	var db *sql.DB
	sutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/trigger" {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if db != nil {
			ctx := context.Background()
			if strings.Contains(string(b), `"id":"ok"`) {
				_, _ = db.ExecContext(ctx, `INSERT INTO users (name) VALUES ('ok')`)
			} else if strings.Contains(string(b), `"id":"bad"`) {
				_, _ = db.ExecContext(ctx, `INSERT INTO users (name) VALUES ('bad')`)
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer sutServer.Close()

	tmpDir := t.TempDir()
	testutil.CreateFile(t, tmpDir, "suite/dojo.yaml", `
concurrency: 2
apis:
  postgres:
    mode: live
    protocol: postgres
    url: "`+connStr+`"
entrypoints:
  webhook:
    type: http
    path: /trigger
    url: "`+sutServer.URL+`"
`)
	testutil.CreateFile(t, tmpDir, "suite/seed/schema.sql",
		"CREATE TABLE IF NOT EXISTS users (id SERIAL PRIMARY KEY, name TEXT);")

	// test_ok: valid seed + unique postgres expectation
	testutil.CreateFile(t, tmpDir, "suite/test_ok/test.plan", `
Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> postgres -> Payload: ""
`)
	testutil.CreateFile(t, tmpDir, "suite/test_ok/incoming.json", `{"id":"ok"}`)
	testutil.CreateFile(t, tmpDir, "suite/test_ok/seed/seed.sql",
		"INSERT INTO users (name) VALUES ('ok');")
	testutil.AppendFile(t, tmpDir, "suite/test_ok/dojo.yaml", `
apis:
  postgres:
    expected_request:
      body: INSERT INTO users (name) VALUES ('ok')
    expected_response:
      body: INSERT 0 1
`)

	// test_bad_seed: invalid SQL in seed
	testutil.CreateFile(t, tmpDir, "suite/test_bad_seed/test.plan", `
Perform -> entrypoints/webhook -> Payload: incoming.json
Expect -> postgres -> Payload: ""
`)
	testutil.CreateFile(t, tmpDir, "suite/test_bad_seed/incoming.json", `{"id":"bad"}`)
	testutil.CreateFile(t, tmpDir, "suite/test_bad_seed/seed/bad.sql", `THIS IS NOT SQL;`)
	testutil.AppendFile(t, tmpDir, "suite/test_bad_seed/dojo.yaml", `
apis:
  postgres:
    expected_request:
      body: INSERT INTO users (name) VALUES ('bad')
    expected_response:
      body: INSERT 0 1
`)

	ws, err := workspace.LoadWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}

	eng := engine.NewEngine(ws)
	if _, err := eng.StartProxies(ctx, "suite"); err != nil {
		t.Fatalf("StartProxies: %v", err)
	}
	defer eng.StopProxies()

	pgProxyURL := "postgres://postgres:postgres@" + eng.PostgresProxy.Addr() + "/postgres?sslmode=disable"
	var errDB error
	db, errDB = sql.Open("postgres", pgProxyURL)
	if errDB != nil {
		t.Fatalf("open db via proxy: %v", errDB)
	}
	defer db.Close()

	suiteCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	summary, err := eng.RunSuite(suiteCtx, "suite", nil)
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	if summary.Passed != 0 {
		t.Fatalf("want 0 passed after seed cascade, got %d (results=%+v)", summary.Passed, summary.Results)
	}
	if summary.Failed != 2 {
		t.Fatalf("want 2 failed, got %d failures=%v", summary.Failed, summary.Failures)
	}
	var sawSeed, sawCascade bool
	for _, f := range summary.Failures {
		if f.TestName == "test_bad_seed" && containsSeedFailure(f.Reason) {
			sawSeed = true
		}
		if f.TestName == "test_ok" && containsCascade(f.Reason) {
			sawCascade = true
		}
	}
	if !sawSeed {
		t.Fatalf("expected test_bad_seed to have seed failure reason, failures=%+v", summary.Failures)
	}
	if !sawCascade {
		t.Fatalf("expected test_ok to have suite-aborted reason, failures=%+v", summary.Failures)
	}
}

func containsSeedFailure(s string) bool {
	return strings.Contains(s, "test seeding failed") || strings.Contains(s, "failed to execute seed")
}

func containsCascade(s string) bool {
	return strings.Contains(s, "suite aborted because seeding failed")
}
