package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// ArtifactRepo implements port.ArtifactRepo against SQLite.
type ArtifactRepo struct{ db *sql.DB }

var _ port.ArtifactRepo = (*ArtifactRepo)(nil)

func (r *ArtifactRepo) Insert(ctx context.Context, a model.Artifact) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO artifacts (id, document_id, filename, content_type, created_job_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.DocumentID, a.Filename, a.ContentType, a.CreatedJobID,
		a.CreatedAt.UTC().Format(time.RFC3339Nano),
		a.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (r *ArtifactRepo) Get(ctx context.Context, documentID, artifactID string) (model.Artifact, error) {
	row := r.db.QueryRowContext(ctx,
		"SELECT * FROM artifacts WHERE document_id=? AND id=?", documentID, artifactID)
	a, err := scanArtifact(row)
	if err == sql.ErrNoRows {
		return model.Artifact{}, fmt.Errorf("artifact not found: %s", artifactID)
	}
	return a, err
}

func (r *ArtifactRepo) ListForDocument(ctx context.Context, documentID string) ([]model.Artifact, error) {
	rows, err := r.db.QueryContext(ctx,
		"SELECT * FROM artifacts WHERE document_id=? ORDER BY created_at ASC", documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var artifacts []model.Artifact
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, a)
	}
	return artifacts, rows.Err()
}

func scanArtifact(row rowScanner) (model.Artifact, error) {
	var (
		a         model.Artifact
		createdAt string
		updatedAt string
	)
	err := row.Scan(
		&a.ID, &a.DocumentID, &a.Filename, &a.ContentType,
		&a.CreatedJobID, &createdAt, &updatedAt,
	)
	if err != nil {
		return model.Artifact{}, err
	}
	a.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	a.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return a, nil
}
