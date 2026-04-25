// migrate-sqlite copies all data from an existing SQLite pipeline.db into a
// PostgreSQL database that already has the schema applied. Safe to re-run —
// every insert uses ON CONFLICT DO NOTHING.
//
// Usage:
//
//	go run ./cmd/migrate-sqlite \
//	  --sqlite  /data/pipeline.db \
//	  --postgres "postgresql://user:pass@host:5432/shared?search_path=document_pipeline"
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"

	_ "github.com/glebarez/go-sqlite"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	sqliteDSN := flag.String("sqlite", "", "path to SQLite pipeline.db file")
	pgDSN := flag.String("postgres", "", "PostgreSQL DSN (search_path must include target schema)")
	flag.Parse()

	if *sqliteDSN == "" || *pgDSN == "" {
		fmt.Fprintln(os.Stderr, "usage: migrate-sqlite --sqlite <path> --postgres <dsn>")
		os.Exit(1)
	}

	ctx := context.Background()

	src, err := sql.Open("sqlite", *sqliteDSN)
	if err != nil {
		slog.Error("open sqlite", "err", err)
		os.Exit(1)
	}
	defer src.Close()

	dst, err := sql.Open("pgx", *pgDSN)
	if err != nil {
		slog.Error("open postgres", "err", err)
		os.Exit(1)
	}
	defer dst.Close()

	if err := run(ctx, src, dst); err != nil {
		slog.Error("migration failed", "err", err)
		os.Exit(1)
	}
	slog.Info("migration complete")
}

func run(ctx context.Context, src, dst *sql.DB) error {
	// FK-safe insertion order.
	tables := []table{
		{
			name:     "documents",
			columns:  []string{"id", "content_hash", "created_at", "updated_at", "title", "date_month", "png_path", "duplicate_of", "additional_context", "linked_contexts"},
			conflict: "id",
		},
		{
			name:     "jobs",
			columns:  []string{"id", "document_id", "stage", "status", "options", "runs", "created_at", "updated_at"},
			conflict: "id",
		},
		{
			name:     "artifacts",
			columns:  []string{"id", "document_id", "filename", "content_type", "created_job_id", "created_at", "updated_at"},
			conflict: "id",
		},
		{
			name:     "stage_events",
			columns:  []string{"id", "document_id", "timestamp", "stage", "event_type", "data"},
			conflict: "id",
		},
		{
			name:     "contexts",
			columns:  []string{"id", "name", "text", "created_at"},
			conflict: "id",
		},
		{
			name:     "key_value",
			columns:  []string{"key", "value"},
			conflict: "key",
		},
		{
			name:     "index_queue",
			columns:  []string{"id", "doc_id", "action", "created_at"},
			conflict: "id",
		},
	}

	for _, t := range tables {
		n, err := migrateTable(ctx, src, dst, t)
		if err != nil {
			return fmt.Errorf("table %s: %w", t.name, err)
		}
		slog.Info("migrated table", "table", t.name, "rows", n)
	}
	return nil
}

type table struct {
	name     string
	columns  []string
	conflict string
}

func migrateTable(ctx context.Context, src, dst *sql.DB, t table) (int, error) {
	cols := joinCols(t.columns)
	rows, err := src.QueryContext(ctx, "SELECT "+cols+" FROM "+t.name)
	if err != nil {
		// Table may not exist in older SQLite DBs (e.g. index_queue added later).
		slog.Warn("skipping table (not found in source)", "table", t.name, "err", err)
		return 0, nil
	}
	defer rows.Close()

	placeholders := make([]string, len(t.columns))
	for i := range t.columns {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	insertSQL := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO NOTHING",
		t.name, cols, joinStrings(placeholders, ", "), t.conflict,
	)

	n := 0
	vals := make([]any, len(t.columns))
	ptrs := make([]any, len(t.columns))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return n, fmt.Errorf("scan: %w", err)
		}
		if _, err := dst.ExecContext(ctx, insertSQL, vals...); err != nil {
			return n, fmt.Errorf("insert row: %w", err)
		}
		n++
	}
	return n, rows.Err()
}

func joinCols(cols []string) string {
	return joinStrings(cols, ", ")
}

func joinStrings(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
