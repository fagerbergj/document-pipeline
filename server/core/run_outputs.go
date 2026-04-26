package core

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/google/uuid"
)

// fieldDraft is the in-memory shape of a stage input/output before the worker
// writes it to the vault and creates an artifact row. The text never persists
// in jobs.runs JSON; only the resulting model.Field (with ArtifactID/Size/Preview)
// does. The full value is fetched via the existing artifact endpoint.
type fieldDraft struct {
	field string
	text  string
	ext   string // "md", "json", "txt"
}

func mdField(field, text string) fieldDraft { return fieldDraft{field: field, text: text, ext: "md"} }
func jsonField(field, text string) fieldDraft {
	return fieldDraft{field: field, text: text, ext: "json"}
}
func txtField(field, text string) fieldDraft { return fieldDraft{field: field, text: text, ext: "txt"} }

func contentTypeFor(ext string) string {
	switch ext {
	case "json":
		return "application/json"
	case "md":
		return "text/markdown"
	default:
		return "text/plain"
	}
}

// runOutputPath returns the vault-relative path for a single field of a run.
// Files live under <vault>/runs/<job>/<run>/<field>.<ext> for organized layout
// while remaining addressable through the artifacts table.
func runOutputPath(jobID, runID, field, ext string) string {
	if ext == "" {
		ext = "md"
	}
	return filepath.Join("runs", jobID, runID, field+"."+ext)
}

// readArtifactText reads the file backing an artifact (by Path if set, else
// by the legacy <vault>/artifacts/<id>/<filename> layout).
func readArtifactText(store interface {
	Read(vaultPath, artifactID, filename string) ([]byte, error)
	ReadAt(vaultPath, relPath string) ([]byte, error)
}, vaultPath string, art model.Artifact) (string, error) {
	if art.Path != nil && *art.Path != "" {
		b, err := store.ReadAt(vaultPath, *art.Path)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	b, err := store.Read(vaultPath, art.ID, art.Filename)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// persistRun writes input/output drafts to disk, creates an artifact row for
// each, and assembles a model.Run referencing them by artifact id.
func (w *WorkerService) persistRun(ctx context.Context, job model.Job, inputs, outputs []fieldDraft, confidence model.Confidence, questions []model.Question) (model.Run, error) {
	now := time.Now().UTC()
	runID := uuid.NewString()
	inFields, err := w.persistDrafts(ctx, job, runID, inputs, now)
	if err != nil {
		return model.Run{}, fmt.Errorf("persist inputs: %w", err)
	}
	outFields, err := w.persistDrafts(ctx, job, runID, outputs, now)
	if err != nil {
		return model.Run{}, fmt.Errorf("persist outputs: %w", err)
	}
	if questions == nil {
		questions = []model.Question{}
	}
	return model.Run{
		ID:         runID,
		Inputs:     inFields,
		Outputs:    outFields,
		Confidence: confidence,
		Questions:  questions,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

func (w *WorkerService) persistDrafts(ctx context.Context, job model.Job, runID string, drafts []fieldDraft, now time.Time) ([]model.Field, error) {
	out := make([]model.Field, 0, len(drafts))
	for _, d := range drafts {
		field := d.field
		if field == "" {
			field = "_unnamed"
		}
		ext := d.ext
		if ext == "" {
			ext = "md"
		}
		relPath := runOutputPath(job.ID, runID, field, ext)
		if err := w.store.SaveAt(w.vaultPath, relPath, []byte(d.text)); err != nil {
			return nil, fmt.Errorf("write run output %s: %w", field, err)
		}
		artifactID := uuid.NewString()
		jobID := job.ID
		path := relPath
		art := model.Artifact{
			ID:           artifactID,
			DocumentID:   job.DocumentID,
			Filename:     field + "." + ext,
			ContentType:  contentTypeFor(ext),
			CreatedJobID: &jobID,
			Path:         &path,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := w.artifacts.Insert(ctx, art); err != nil {
			return nil, fmt.Errorf("insert artifact for %s: %w", field, err)
		}
		out = append(out, model.Field{
			Field:      field,
			ArtifactID: artifactID,
			Size:       int64(len(d.text)),
			Preview:    previewOf(d.text),
		})
	}
	return out, nil
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
