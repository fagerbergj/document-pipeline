package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// DocumentRepo implements port.DocumentRepo against SQLite.
type DocumentRepo struct{ db *sql.DB }

var _ port.DocumentRepo = (*DocumentRepo)(nil)

func (r *DocumentRepo) Insert(ctx context.Context, doc model.Document) error {
	linkedJSON, err := json.Marshal(doc.LinkedContexts)
	if err != nil {
		return fmt.Errorf("marshal linked_contexts: %w", err)
	}
	_, err = r.db.ExecContext(ctx, q["documents.Insert"],
		doc.ID, doc.ContentHash,
		doc.CreatedAt.UTC().Format(time.RFC3339Nano),
		doc.UpdatedAt.UTC().Format(time.RFC3339Nano),
		doc.Title, doc.DateMonth, doc.PNGPath, doc.DuplicateOf,
		doc.AdditionalContext, string(linkedJSON),
	)
	return err
}

func (r *DocumentRepo) Get(ctx context.Context, id string) (model.Document, error) {
	row := r.db.QueryRowContext(ctx, q["documents.Get"], id)
	doc, err := scanDocument(row)
	if err == sql.ErrNoRows {
		return model.Document{}, fmt.Errorf("document not found: %s", id)
	}
	return doc, err
}

func (r *DocumentRepo) GetByHash(ctx context.Context, hash string) (model.Document, bool, error) {
	row := r.db.QueryRowContext(ctx, q["documents.GetByHash"], hash)
	doc, err := scanDocument(row)
	if err == sql.ErrNoRows {
		return model.Document{}, false, nil
	}
	if err != nil {
		return model.Document{}, false, err
	}
	return doc, true, nil
}

func (r *DocumentRepo) Update(ctx context.Context, doc model.Document) error {
	linkedJSON, err := json.Marshal(doc.LinkedContexts)
	if err != nil {
		return fmt.Errorf("marshal linked_contexts: %w", err)
	}
	_, err = r.db.ExecContext(ctx, q["documents.Update"],
		doc.UpdatedAt.UTC().Format(time.RFC3339Nano),
		doc.Title, doc.DateMonth, doc.PNGPath, doc.DuplicateOf,
		doc.AdditionalContext, string(linkedJSON), doc.ID,
	)
	return err
}

func (r *DocumentRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, q["documents.Delete"], id)
	return err
}

func (r *DocumentRepo) ListPaginated(ctx context.Context, filter port.DocumentFilter, page model.PageRequest) (model.PageResult[model.Document], error) {
	sc, ok := docSortMap[filter.Sort]
	if !ok {
		sc = docSortMap["pipeline"]
	}

	// Current-job subquery for stage/status filtering.
	const currentJobSQL = `(
		SELECT j.%s FROM jobs j WHERE j.document_id = d.id
		ORDER BY CASE j.status
			WHEN 'running'  THEN 0 WHEN 'waiting' THEN 1
			WHEN 'pending'  THEN 2 WHEN 'error'   THEN 3
			ELSE 4 END, j.updated_at DESC
		LIMIT 1
	)`

	conditions := []string{}
	params := []any{}

	if len(filter.Stages) > 0 {
		conditions = append(conditions, fmt.Sprintf(currentJobSQL, "stage")+" IN "+inClause(len(filter.Stages)))
		for _, s := range filter.Stages {
			params = append(params, s)
		}
	}
	if len(filter.Statuses) > 0 {
		conditions = append(conditions, fmt.Sprintf(currentJobSQL, "status")+" IN "+inClause(len(filter.Statuses)))
		for _, s := range filter.Statuses {
			params = append(params, s)
		}
	}
	if page.PageToken != nil {
		conditions = append(conditions, sc.cursorWhere)
		params = append(params, page.PageToken.SortKey, page.PageToken.LastID)
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	limit := page.PageSize
	if limit <= 0 {
		limit = 20
	}
	params = append(params, limit+1)

	stmt := fmt.Sprintf("SELECT d.* FROM documents d %s ORDER BY %s LIMIT ?", where, sc.order)
	rows, err := r.db.QueryContext(ctx, stmt, params...)
	if err != nil {
		return model.PageResult[model.Document]{}, err
	}
	defer rows.Close()

	docs, err := scanDocuments(rows)
	if err != nil {
		return model.PageResult[model.Document]{}, err
	}

	hasMore := len(docs) > limit
	if hasMore {
		docs = docs[:limit]
	}

	var nextToken *string
	if hasMore && len(docs) > 0 {
		last := docs[len(docs)-1]
		nextToken = encodeToken(last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID)
	}

	return model.PageResult[model.Document]{Data: docs, NextPageToken: nextToken}, nil
}

// ── scan helpers ──────────────────────────────────────────────────────────────

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDocument(row rowScanner) (model.Document, error) {
	var (
		d          model.Document
		createdAt  string
		updatedAt  string
		linkedJSON string
	)
	err := row.Scan(
		&d.ID, &d.ContentHash,
		&createdAt, &updatedAt,
		&d.Title, &d.DateMonth, &d.PNGPath, &d.DuplicateOf,
		&d.AdditionalContext, &linkedJSON,
	)
	if err != nil {
		return model.Document{}, err
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	d.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if linkedJSON != "" {
		json.Unmarshal([]byte(linkedJSON), &d.LinkedContexts)
	}
	if d.LinkedContexts == nil {
		d.LinkedContexts = []string{}
	}
	return d, nil
}

func scanDocuments(rows *sql.Rows) ([]model.Document, error) {
	var docs []model.Document
	for rows.Next() {
		doc, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}
