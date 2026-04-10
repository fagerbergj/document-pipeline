package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// KeyValueRepo implements port.KeyValueRepo against SQLite.
type KeyValueRepo struct{ db *sql.DB }

var _ port.KeyValueRepo = (*KeyValueRepo)(nil)

func (r *KeyValueRepo) Set(ctx context.Context, key, value string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO key_value (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value)
	return err
}

func (r *KeyValueRepo) Get(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := r.db.QueryRowContext(ctx, "SELECT value FROM key_value WHERE key=?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("key_value get %q: %w", key, err)
	}
	return value, true, nil
}
