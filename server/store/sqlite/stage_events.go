package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// StageEventRepo implements port.StageEventRepo against SQLite.
type StageEventRepo struct{ db *sql.DB }

var _ port.StageEventRepo = (*StageEventRepo)(nil)

func (r *StageEventRepo) Append(ctx context.Context, event model.StageEvent) error {
	_, err := r.db.ExecContext(ctx,
		"INSERT INTO stage_events (document_id, timestamp, stage, event_type) VALUES (?, ?, ?, ?)",
		event.DocumentID,
		event.Timestamp.UTC().Format(time.RFC3339Nano),
		event.Stage,
		string(event.EventType),
	)
	return err
}

func (r *StageEventRepo) CountFailures(ctx context.Context, documentID, stage string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM stage_events WHERE document_id=? AND stage=? AND event_type='failed'",
		documentID, stage,
	).Scan(&count)
	return count, err
}
