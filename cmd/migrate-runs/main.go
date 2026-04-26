// migrate-runs externalizes inline run output text from the jobs.runs JSON
// column into vault files + artifact rows. Idempotent — runs already in the
// new shape (Field has artifact_id but no text) are skipped.
//
// Usage:
//
//	go run ./cmd/migrate-runs \
//	  --postgres "postgresql://user:pass@host:5432/db?search_path=document_pipeline" \
//	  --vault    /data/vault
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/google/uuid"
)

// legacyField mirrors the pre-PR-1 Field shape (with inline Text). The new
// shape adds ArtifactID/Size/Preview and drops Text.
type legacyField struct {
	Field      string `json:"field"`
	Text       string `json:"text,omitempty"`
	ArtifactID string `json:"artifact_id,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Preview    string `json:"preview,omitempty"`
}

type legacyRun struct {
	ID         string        `json:"ID"`
	Inputs     []legacyField `json:"Inputs"`
	Outputs    []legacyField `json:"Outputs"`
	Confidence string        `json:"Confidence"`
	Questions  any           `json:"Questions"`
	CreatedAt  time.Time     `json:"CreatedAt"`
	UpdatedAt  time.Time     `json:"UpdatedAt"`
}

func main() {
	pgDSN := flag.String("postgres", "", "PostgreSQL DSN")
	vault := flag.String("vault", "", "vault path (where runs/ and artifacts/ live)")
	flag.Parse()

	if *pgDSN == "" || *vault == "" {
		fmt.Fprintln(os.Stderr, "usage: migrate-runs --postgres <dsn> --vault <path>")
		os.Exit(1)
	}

	ctx := context.Background()
	db, err := sql.Open("pgx", *pgDSN)
	if err != nil {
		slog.Error("open postgres", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, "SELECT id, document_id, runs FROM jobs")
	if err != nil {
		slog.Error("query jobs", "err", err)
		os.Exit(1)
	}
	defer rows.Close()

	var processed, rewritten int
	for rows.Next() {
		var jobID, docID, runsJSON string
		if err := rows.Scan(&jobID, &docID, &runsJSON); err != nil {
			slog.Error("scan row", "err", err)
			continue
		}
		processed++
		if runsJSON == "" || runsJSON == "[]" {
			continue
		}

		var runs []legacyRun
		if err := json.Unmarshal([]byte(runsJSON), &runs); err != nil {
			slog.Warn("unmarshal runs (skipping job)", "job_id", jobID[:8], "err", err)
			continue
		}

		changed := false
		for ri := range runs {
			if migrateFields(ctx, db, *vault, jobID, docID, runs[ri].ID, runs[ri].Inputs) {
				changed = true
			}
			if migrateFields(ctx, db, *vault, jobID, docID, runs[ri].ID, runs[ri].Outputs) {
				changed = true
			}
		}

		if !changed {
			continue
		}

		newJSON, err := json.Marshal(runs)
		if err != nil {
			slog.Error("marshal runs", "job_id", jobID[:8], "err", err)
			continue
		}
		if _, err := db.ExecContext(ctx, "UPDATE jobs SET runs = $1 WHERE id = $2", string(newJSON), jobID); err != nil {
			slog.Error("update runs", "job_id", jobID[:8], "err", err)
			continue
		}
		rewritten++
		slog.Info("migrated job", "job_id", jobID[:8], "runs", len(runs))
	}
	if err := rows.Err(); err != nil {
		slog.Error("iterate jobs", "err", err)
		os.Exit(1)
	}

	slog.Info("done", "jobs_processed", processed, "jobs_rewritten", rewritten)
}

// migrateFields converts each legacy Field with inline Text into a new-shape
// Field backed by a vault file + artifact row. Returns true if anything changed.
func migrateFields(ctx context.Context, db *sql.DB, vault, jobID, docID, runID string, fields []legacyField) bool {
	changed := false
	for i := range fields {
		f := &fields[i]
		if f.ArtifactID != "" || f.Text == "" {
			continue // already migrated or nothing to externalize
		}
		fieldName := f.Field
		if fieldName == "" {
			fieldName = "_unnamed"
		}
		ext := "md"
		relPath := filepath.Join("runs", jobID, runID, fieldName+"."+ext)
		full := filepath.Join(vault, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			slog.Warn("mkdir", "path", full, "err", err)
			continue
		}
		if err := os.WriteFile(full, []byte(f.Text), 0o644); err != nil {
			slog.Warn("write file", "path", full, "err", err)
			continue
		}
		artID := uuid.NewString()
		now := time.Now().UTC()
		jobIDCopy := jobID
		_, err := db.ExecContext(ctx, `INSERT INTO artifacts
			(id, document_id, filename, content_type, created_job_id, path, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			artID, docID, fieldName+"."+ext, "text/markdown", jobIDCopy, relPath,
			now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
		)
		if err != nil {
			slog.Warn("insert artifact", "field", fieldName, "err", err)
			continue
		}
		size := int64(len(f.Text))
		preview := previewOf(f.Text)

		f.ArtifactID = artID
		f.Size = size
		f.Preview = preview
		f.Text = ""
		changed = true
	}
	return changed
}

func previewOf(s string) string {
	const max = 200
	if len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
