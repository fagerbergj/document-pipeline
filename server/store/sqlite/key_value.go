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
	_, err := r.db.ExecContext(ctx, q["key_value.Set"], key, value)
	return err
}

func (r *KeyValueRepo) Get(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := r.db.QueryRowContext(ctx, q["key_value.Get"], key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("key_value get %q: %w", key, err)
	}
	return value, true, nil
}
