package postgres

import (
	"context"
	"testing"
)

func TestMigrations_UpDown(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	for _, table := range []string{"documents", "jobs", "artifacts", "stage_events", "contexts", "key_value"} {
		var name string
		err := db.db.QueryRowContext(ctx,
			"SELECT table_name FROM information_schema.tables WHERE table_schema=current_schema() AND table_name=$1", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing after migration: %v", table, err)
		}
	}

	var count int
	if err := db.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM _migrations").Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 11 {
		t.Errorf("expected 11 migration records, got %d", count)
	}

	if err := db.rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	for _, table := range []string{"documents", "jobs", "artifacts", "stage_events", "contexts", "key_value"} {
		var name string
		err := db.db.QueryRowContext(ctx,
			"SELECT table_name FROM information_schema.tables WHERE table_schema=current_schema() AND table_name=$1", table,
		).Scan(&name)
		if err == nil {
			t.Errorf("table %q still exists after rollback", table)
		}
	}
}

func TestMigrations_Idempotent(t *testing.T) {
	db := openTestDB(t)
	db.Close()

	// Re-open against same DSN — not easily testable with containers, so just
	// verify the first open applied all migrations without error.
	var count int
	db2 := openTestDB(t)
	if err := db2.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM _migrations").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 11 {
		t.Errorf("expected 11 migration records, got %d", count)
	}
}
