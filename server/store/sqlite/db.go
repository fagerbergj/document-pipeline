package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps a *sql.DB with migration support.
type DB struct {
	db            *sql.DB
	migrationsDir string
}

// Open opens (or creates) the SQLite database at dbPath, enables WAL mode,
// and applies any pending up-migrations found in migrationsDir.
func Open(dbPath, migrationsDir string) (*DB, error) {
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Single writer, many readers.
	conn.SetMaxOpenConns(1)

	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
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

// Repos returns all repository implementations backed by this DB.
func (d *DB) Documents() *DocumentRepo       { return &DocumentRepo{db: d.db} }
func (d *DB) Jobs() *JobRepo                 { return &JobRepo{db: d.db} }
func (d *DB) Artifacts() *ArtifactRepo       { return &ArtifactRepo{db: d.db} }
func (d *DB) StageEvents() *StageEventRepo   { return &StageEventRepo{db: d.db} }
func (d *DB) Contexts() *ContextRepo         { return &ContextRepo{db: d.db} }
func (d *DB) Chats() *ChatRepo               { return &ChatRepo{db: d.db} }
func (d *DB) ChatMessages() *ChatMessageRepo { return &ChatMessageRepo{db: d.db} }
func (d *DB) KeyValues() *KeyValueRepo       { return &KeyValueRepo{db: d.db} }

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
			"SELECT COUNT(*) FROM _migrations WHERE name = ?", name,
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
			"INSERT INTO _migrations (name, applied_at) VALUES (?, ?)",
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
