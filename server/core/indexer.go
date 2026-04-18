package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// IndexerService polls the index_queue table and keeps OpenSearch in sync with SQLite.
type IndexerService struct {
	db      *sql.DB
	docs    port.DocumentRepo
	jobs    port.JobRepo
	indexer port.DocumentIndexer
}

func NewIndexerService(db *sql.DB, docs port.DocumentRepo, jobs port.JobRepo, indexer port.DocumentIndexer) *IndexerService {
	return &IndexerService{db: db, docs: docs, jobs: jobs, indexer: indexer}
}

// Run starts the indexer loop. It blocks until ctx is cancelled.
func (s *IndexerService) Run(ctx context.Context) {
	slog.Info("indexer started")

	if err := s.backfillIfEmpty(ctx); err != nil {
		slog.Warn("indexer backfill failed", "err", err)
	}

	for {
		select {
		case <-ctx.Done():
			slog.Info("indexer shutting down")
			return
		case <-time.After(2 * time.Second):
			if err := s.processQueue(ctx); err != nil {
				slog.Warn("indexer process queue", "err", err)
			}
		}
	}
}

// backfillIfEmpty enqueues all existing documents when the OpenSearch index is empty.
func (s *IndexerService) backfillIfEmpty(ctx context.Context) error {
	count, err := s.indexer.Count(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	rows, err := s.db.QueryContext(ctx, "SELECT id FROM documents")
	if err != nil {
		return err
	}
	defer rows.Close()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	n := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO index_queue (doc_id, action) VALUES (?, 'index')", id); err != nil {
			tx.Rollback()
			return err
		}
		n++
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if n > 0 {
		slog.Info("indexer backfill enqueued", "count", n)
	}
	return nil
}

// processQueue handles up to 50 deduplicated entries per cycle.
func (s *IndexerService) processQueue(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, doc_id, action FROM index_queue AS iq
		WHERE id = (SELECT MAX(id) FROM index_queue WHERE doc_id = iq.doc_id)
		GROUP BY doc_id ORDER BY id ASC LIMIT 50
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type entry struct {
		id     int64
		docID  string
		action string
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.id, &e.docID, &e.action); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	rows.Close()

	for _, e := range entries {
		var processErr error
		if e.action == "delete" {
			processErr = s.indexer.Delete(ctx, e.docID)
		} else {
			processErr = s.indexDoc(ctx, e.docID)
		}
		if processErr != nil {
			slog.Warn("indexer entry failed", "doc_id", e.docID[:8], "action", e.action, "err", processErr)
			continue
		}
		s.db.ExecContext(ctx, "DELETE FROM index_queue WHERE doc_id = ? AND id <= ?", e.docID, e.id)
	}
	return nil
}

func (s *IndexerService) indexDoc(ctx context.Context, docID string) error {
	doc, err := s.docs.Get(ctx, docID)
	if err != nil {
		return err
	}

	stageData, err := CollectStageData(ctx, s.jobs, docID)
	if err != nil {
		return err
	}

	content := stringField(stageData, "clarify", "clarified_text")
	summary := stringField(stageData, "classify", "summary")
	tags := parseTagsField(stageData, "classify", "tags")

	jobs, err := s.jobs.ListForDocument(ctx, docID)
	if err != nil {
		return err
	}
	current := PickCurrentJob(jobs)

	idoc := port.IndexDoc{
		DocID:     docID,
		Content:   content,
		Summary:   summary,
		Tags:      tags,
		DateMonth: ptrStr(doc.DateMonth),
	}
	if doc.Title != nil {
		idoc.Title = *doc.Title
	}
	if doc.Series != nil {
		idoc.Series = *doc.Series
	}
	if current != nil {
		idoc.Stage = current.Stage
		idoc.Status = string(current.Status)
	}

	return s.indexer.Index(ctx, idoc)
}

func stringField(stageData map[string]map[string]any, stage, field string) string {
	if sd, ok := stageData[stage]; ok {
		if v, ok := sd[field].(string); ok {
			return v
		}
	}
	return ""
}

func parseTagsField(stageData map[string]map[string]any, stage, field string) []string {
	raw := stringField(stageData, stage, field)
	if raw == "" {
		return nil
	}
	raw = strings.TrimSpace(raw)
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil
	}
	return tags
}

func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
