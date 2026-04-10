package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/google/uuid"
)

// ContextRepo implements port.ContextRepo against SQLite.
type ContextRepo struct{ db *sql.DB }

var _ port.ContextRepo = (*ContextRepo)(nil)

func (r *ContextRepo) List(ctx context.Context) ([]model.Context, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT id, name, text, created_at FROM contexts ORDER BY created_at ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []model.Context
	for rows.Next() {
		e, err := scanContext(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (r *ContextRepo) Create(ctx context.Context, name, text string) (model.Context, error) {
	id := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.ExecContext(ctx,
		"INSERT INTO contexts (id, name, text, created_at) VALUES (?, ?, ?, ?)",
		id, name, text, now)
	if err != nil {
		return model.Context{}, err
	}
	t, _ := time.Parse(time.RFC3339Nano, now)
	return model.Context{ID: id, Name: name, Text: text, CreatedAt: t}, nil
}

func (r *ContextRepo) Update(ctx context.Context, id string, name, text *string) (model.Context, error) {
	sets := []string{}
	params := []any{}
	if name != nil {
		sets = append(sets, "name=?")
		params = append(params, *name)
	}
	if text != nil {
		sets = append(sets, "text=?")
		params = append(params, *text)
	}
	if len(sets) > 0 {
		params = append(params, id)
		_, err := r.db.ExecContext(ctx,
			"UPDATE contexts SET "+strings.Join(sets, ", ")+" WHERE id=?", params...)
		if err != nil {
			return model.Context{}, err
		}
	}

	row := r.db.QueryRowContext(ctx, "SELECT id, name, text, created_at FROM contexts WHERE id=?", id)
	e, err := scanContext(row)
	if err == sql.ErrNoRows {
		return model.Context{}, fmt.Errorf("context not found: %s", id)
	}
	return e, err
}

func (r *ContextRepo) Delete(ctx context.Context, id string) (bool, error) {
	res, err := r.db.ExecContext(ctx, "DELETE FROM contexts WHERE id=?", id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func scanContext(row rowScanner) (model.Context, error) {
	var (
		e         model.Context
		createdAt string
	)
	err := row.Scan(&e.ID, &e.Name, &e.Text, &createdAt)
	if err != nil {
		return model.Context{}, err
	}
	e.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return e, nil
}
