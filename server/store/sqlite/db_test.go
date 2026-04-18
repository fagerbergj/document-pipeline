package sqlite

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
)

// migrationsDir returns the absolute path to db/migrations relative to this file.
func migrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile is .../server/store/sqlite/db_test.go
	// db/migrations is at repo_root/db/migrations
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	return filepath.Join(root, "db", "migrations")
}

func TestMigrations_UpDown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(path, migrationsDir(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Verify all tables exist after up migrations.
	tables := []string{
		"documents", "jobs", "artifacts", "stage_events",
		"contexts", "chat_sessions", "chat_messages", "key_value",
	}
	for _, table := range tables {
		var name string
		err := db.db.QueryRowContext(ctx,
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing after migration: %v", table, err)
		}
	}

	// Verify migration tracking.
	var count int
	if err := db.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM _migrations").Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 9 {
		t.Errorf("expected 8 migration records, got %d", count)
	}

	// Roll back and verify tables are gone.
	if err := db.rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	for _, table := range tables {
		var name string
		err := db.db.QueryRowContext(ctx,
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err == nil {
			t.Errorf("table %q still exists after rollback", table)
		}
	}
}

func TestMigrations_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	md := migrationsDir(t)

	// Open twice — second open should find all migrations already applied.
	db1, err := Open(path, md)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	db1.Close()

	db2, err := Open(path, md)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer db2.Close()

	var count int
	if err := db2.db.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM _migrations").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 9 {
		t.Errorf("expected 8 migration records on second open, got %d", count)
	}
}
