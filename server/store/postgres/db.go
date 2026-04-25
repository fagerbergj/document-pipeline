package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// DB wraps a *sql.DB with migration support.
type DB struct {
	db            *sql.DB
	migrationsDir string
}

// Open connects to the PostgreSQL database at dsn, ensures the schema named in
// search_path exists, and applies any pending up-migrations from migrationsDir.
func Open(dsn, migrationsDir string) (*DB, error) {
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	ctx := context.Background()

	// Create the schema before the migration runner creates _migrations so that
	// all tables (including _migrations) land in the right schema.
	if schema := searchPathFromDSN(dsn); schema != "" {
		if _, err := conn.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema); err != nil {
			conn.Close()
			return nil, fmt.Errorf("create schema %q: %w", schema, err)
		}
	}

	d := &DB{db: conn, migrationsDir: migrationsDir}
	if err := d.migrate(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

// Close closes the underlying database connection.
func (d *DB) Close() error {
	return d.db.Close()
}

// DB returns the underlying *sql.DB (e.g. for direct queue queries in IndexerService).
func (d *DB) DB() *sql.DB { return d.db }

// Repos returns all repository implementations backed by this DB.
func (d *DB) Documents() *DocumentRepo     { return &DocumentRepo{db: d.db} }
func (d *DB) Jobs() *JobRepo               { return &JobRepo{db: d.db} }
func (d *DB) Artifacts() *ArtifactRepo     { return &ArtifactRepo{db: d.db} }
func (d *DB) StageEvents() *StageEventRepo { return &StageEventRepo{db: d.db} }
func (d *DB) Contexts() *ContextRepo       { return &ContextRepo{db: d.db} }
func (d *DB) KeyValues() *KeyValueRepo     { return &KeyValueRepo{db: d.db} }

// migrate creates the _migrations tracking table and applies any unapplied
// *.up.sql files from migrationsDir in lexicographic (numeric) order.
func (d *DB) migrate(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS _migrations (
			name       TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`)
	if err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	entries, err := filepath.Glob(filepath.Join(d.migrationsDir, "*.up.sql"))
	if err != nil {
		return fmt.Errorf("glob migrations: %w", err)
	}
	sort.Strings(entries)

	for _, path := range entries {
		name := migrationName(path)

		var exists int
		if err := d.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM _migrations WHERE name = $1", name,
		).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if exists > 0 {
			continue
		}

		sql, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		if _, err := d.db.ExecContext(ctx, string(sql)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}

		if _, err := d.db.ExecContext(ctx,
			"INSERT INTO _migrations (name, applied_at) VALUES ($1, $2)",
			name, time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
	}
	return nil
}

// rollback applies all *.down.sql files in reverse order.
// Used only in tests.
func (d *DB) rollback(ctx context.Context) error {
	entries, err := filepath.Glob(filepath.Join(d.migrationsDir, "*.down.sql"))
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(entries)))

	for _, path := range entries {
		sql, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if _, err := d.db.ExecContext(ctx, string(sql)); err != nil {
			return fmt.Errorf("rollback %s: %w", filepath.Base(path), err)
		}
	}
	_, err = d.db.ExecContext(ctx, "DROP TABLE IF EXISTS _migrations")
	return err
}

// migrationName returns the bare filename without directory or extension.
func migrationName(path string) string {
	name := filepath.Base(path)
	return strings.TrimSuffix(name, ".up.sql")
}

// rebind converts SQLite-style ? placeholders to Postgres $1, $2, … positional
// parameters. Call this on every query string before passing it to the driver.
func rebind(query string) string {
	var b strings.Builder
	n := 0
	for _, c := range query {
		if c == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// searchPathFromDSN extracts the search_path query parameter from a Postgres DSN.
func searchPathFromDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return ""
	}
	return u.Query().Get("search_path")
}
