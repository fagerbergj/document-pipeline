package postgres

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
	_, err := r.db.ExecContext(ctx, q["artifacts.Insert"],
		a.ID, a.DocumentID, a.Filename, a.ContentType, a.CreatedJobID, a.Path,
		a.CreatedAt.UTC().Format(time.RFC3339Nano),
		a.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (r *ArtifactRepo) Get(ctx context.Context, documentID, artifactID string) (model.Artifact, error) {
	row := r.db.QueryRowContext(ctx, q["artifacts.Get"], documentID, artifactID)
	a, err := scanArtifact(row)
	if err == sql.ErrNoRows {
		return model.Artifact{}, fmt.Errorf("artifact not found: %s", artifactID)
	}
	return a, err
}

func (r *ArtifactRepo) ListForDocument(ctx context.Context, documentID string) ([]model.Artifact, error) {
	rows, err := r.db.QueryContext(ctx, q["artifacts.ListForDocument"], documentID)
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

// ListPaginated walks all artifacts via cursor pagination keyed on (created_at, id).
// Filter is currently empty; the cursor pattern matches JobRepo.ListPaginated.
func (r *ArtifactRepo) ListPaginated(ctx context.Context, _ port.ArtifactFilter, page model.PageRequest) (model.PageResult[model.Artifact], error) {
	limit := page.PageSize
	if limit <= 0 {
		limit = 100
	}
	var (
		rows *sql.Rows
		err  error
	)
	if page.PageToken != nil {
		rows, err = r.db.QueryContext(ctx, rebind(
			"SELECT * FROM artifacts WHERE (created_at, id) > (?, ?) ORDER BY created_at, id LIMIT ?"),
			page.PageToken.SortKey, page.PageToken.LastID, limit+1)
	} else {
		rows, err = r.db.QueryContext(ctx, rebind(
			"SELECT * FROM artifacts ORDER BY created_at, id LIMIT ?"), limit+1)
	}
	if err != nil {
		return model.PageResult[model.Artifact]{}, err
	}
	defer rows.Close()

	var out []model.Artifact
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return model.PageResult[model.Artifact]{}, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return model.PageResult[model.Artifact]{}, err
	}

	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	var nextToken *string
	if hasMore && len(out) > 0 {
		last := out[len(out)-1]
		nextToken = encodeToken(last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID)
	}
	return model.PageResult[model.Artifact]{Data: out, NextPageToken: nextToken}, nil
}

func (r *ArtifactRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, rebind("DELETE FROM artifacts WHERE id = ?"), id)
	return err
}

func scanArtifact(row rowScanner) (model.Artifact, error) {
	var (
		a         model.Artifact
		createdAt string
		updatedAt string
	)
	err := row.Scan(
		&a.ID, &a.DocumentID, &a.Filename, &a.ContentType,
		&a.CreatedJobID, &createdAt, &updatedAt, &a.Path,
	)
	if err != nil {
		return model.Artifact{}, err
	}
	a.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	a.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return a, nil
}
