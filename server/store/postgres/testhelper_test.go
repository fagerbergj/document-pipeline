package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// migrationsDir returns the absolute path to db/migrations relative to this file.
func migrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	return filepath.Join(root, "db", "migrations")
}

var (
	sharedDSNOnce sync.Once
	sharedDSN     string
	sharedDSNErr  error
)

// ensureSharedPostgres lazily starts a single Postgres container for the
// package and returns its base DSN (no search_path). The container is reaped
// by testcontainers' ryuk when the test process exits.
func ensureSharedPostgres(t *testing.T) string {
	t.Helper()
	sharedDSNOnce.Do(func() {
		ctx := context.Background()
		ctr, err := tcpostgres.Run(ctx,
			"postgres:17-alpine",
			tcpostgres.WithDatabase("testdb"),
			tcpostgres.WithUsername("test"),
			tcpostgres.WithPassword("test"),
			tcpostgres.BasicWaitStrategies(),
			tcpostgres.WithSQLDriver("pgx"),
		)
		if err != nil {
			sharedDSNErr = fmt.Errorf("start postgres container: %w", err)
			return
		}
		dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			sharedDSNErr = fmt.Errorf("get connection string: %w", err)
			return
		}
		sharedDSN = dsn
	})
	if sharedDSNErr != nil {
		t.Fatal(sharedDSNErr)
	}
	return sharedDSN
}

// openTestDB returns a *DB pinned to a fresh, isolated schema in the shared
// Postgres container. Migrations run into that schema; the schema is dropped
// when the test ends.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	base := ensureSharedPostgres(t)

	schema := fmt.Sprintf("test_%d_%d", time.Now().UnixNano(), rand.Intn(1<<16))
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	dsn := base + sep + "search_path=" + schema

	db, err := Open(dsn, migrationsDir(t))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		admin, err := sql.Open("pgx", base)
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	})
	return db
}
