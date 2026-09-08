package engine

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/elmacnifico/dojo/internal/testutil"
	"github.com/elmacnifico/dojo/internal/workspace"
)

// TestRunSeedsTransactional pins the guarantee that a seed script whose second
// statement fails leaves NO partial state: each seed script executes via a
// single simple-query message, which Postgres wraps in one implicit
// transaction (all-or-nothing). Fully successful scripts commit normally.
// This documents behavior the engine relies on — if the execution strategy
// ever changes (e.g. to per-statement Exec), this test will catch the loss of
// script-level atomicity.
func TestRunSeedsTransactional(t *testing.T) {
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

	e := NewEngine(&workspace.Workspace{BaseDir: t.TempDir()})

	// Failing script: first statement succeeds, second fails (duplicate table).
	badDir := t.TempDir()
	testutil.CreateFile(t, badDir, "01_setup.sql",
		"CREATE TABLE seed_txn_test (id int); INSERT INTO seed_txn_test VALUES (1);\nCREATE TABLE seed_txn_test (id int);")

	if err := e.runSeeds(connStr, badDir); err == nil {
		t.Fatal("expected seed script with failing statement to error")
	}

	// The failed script must have been rolled back entirely.
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()
	if err := db.QueryRow("SELECT COUNT(*) FROM seed_txn_test").Err(); err == nil {
		t.Error("expected seed_txn_test to be absent (rolled back), but the table exists")
	}

	// Successful scripts still commit.
	okDir := t.TempDir()
	testutil.CreateFile(t, okDir, "01_ok.sql", "CREATE TABLE seed_txn_ok (id int); INSERT INTO seed_txn_ok VALUES (7);")
	if err := e.runSeeds(connStr, okDir); err != nil {
		t.Fatalf("expected successful seed script to commit, got: %v", err)
	}
	var got int
	if err := db.QueryRow("SELECT id FROM seed_txn_ok WHERE id = 7").Scan(&got); err != nil {
		t.Fatalf("expected committed row from successful seed: %v", err)
	}
	if got != 7 {
		t.Errorf("expected id 7, got %d", got)
	}
}
