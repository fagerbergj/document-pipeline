package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/google/uuid"
)

var genericFilenames = map[string]bool{
	"remarkable": true,
	"untitled":   true,
	"image":      true,
	"attachment": true,
	"document":   true,
}

// IngestService creates documents from incoming files.
type IngestService struct {
	docs      port.DocumentRepo
	jobs      port.JobRepo
	artifacts port.ArtifactRepo
	events    port.StageEventRepo
	kv        port.KeyValueRepo
	store     port.DocumentArtifactStore
	pipeline  model.PipelineConfig
	vaultPath string
}

func NewIngestService(
	docs port.DocumentRepo,
	jobs port.JobRepo,
	artifacts port.ArtifactRepo,
	events port.StageEventRepo,
	kv port.KeyValueRepo,
	store port.DocumentArtifactStore,
	pipeline model.PipelineConfig,
	vaultPath string,
) *IngestService {
	return &IngestService{docs, jobs, artifacts, events, kv, store, pipeline, vaultPath}
}

// IngestMeta is the webhook ingest metadata stored in kv for the worker.
type IngestMeta struct {
	Meta               map[string]any `json:"meta"`
	AttachmentFilename string         `json:"attachment_filename"`
	RawText            string         `json:"raw_text,omitempty"`
	FileType           string         `json:"file_type,omitempty"`
}

// IngestWebhook ingests a PNG from the reMarkable webhook.
// Returns (job, nil) on success, (zero, nil) on duplicate, (zero, err) on failure.
func (s *IngestService) IngestWebhook(ctx context.Context, imageBytes []byte, meta map[string]any, attachmentFilename string) (model.Job, bool, error) {
	hash := ContentHash(imageBytes)

	_, isDuplicate, err := s.docs.GetByHash(ctx, hash)
	if err != nil {
		return model.Job{}, false, fmt.Errorf("check duplicate: %w", err)
	}
	if isDuplicate {
		slog.Info("duplicate hash — skipping webhook ingest", "hash_prefix", hash[:8])
		return model.Job{}, false, nil
	}

	now := time.Now().UTC()
	artifactID := uuid.NewString()
	filename := hash[:8] + ".png"

	if err := s.store.Save(s.vaultPath, artifactID, filename, imageBytes); err != nil {
		return model.Job{}, false, fmt.Errorf("save artifact: %w", err)
	}
	pngPath := filepath.Join(s.vaultPath, "artifacts", artifactID, filename)

	title := titleFromWebhookMeta(meta, attachmentFilename)
	doc := model.Document{
		ID:          uuid.NewString(),
		ContentHash: hash,
		PNGPath:     &pngPath,
		Title:       title,
		DateMonth:   strPtr(now.Format("2006-01")),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.docs.Insert(ctx, doc); err != nil {
		return model.Job{}, false, fmt.Errorf("insert document: %w", err)
	}

	artifact := model.Artifact{
		ID:          artifactID,
		DocumentID:  doc.ID,
		Filename:    filename,
		ContentType: "image/png",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.artifacts.Insert(ctx, artifact); err != nil {
		return model.Job{}, false, fmt.Errorf("insert artifact: %w", err)
	}

	ingestMeta := IngestMeta{Meta: meta, AttachmentFilename: attachmentFilename}
	if err := s.storeIngestMeta(ctx, doc.ID, ingestMeta); err != nil {
		return model.Job{}, false, err
	}

	if err := s.events.Append(ctx, model.StageEvent{
		DocumentID: doc.ID,
		Stage:      "webhook",
		EventType:  model.EventReceived,
		Timestamp:  now,
	}); err != nil {
		return model.Job{}, false, fmt.Errorf("append event: %w", err)
	}

	job, err := s.createFirstJob(ctx, doc, now)
	if err != nil {
		return model.Job{}, false, err
	}

	slog.Info("created document via webhook", "doc_id", doc.ID[:8], "hash_prefix", hash[:8])
	return job, true, nil
}

// IngestUpload ingests a file from a direct API upload.
// Returns (job, true, nil) on success, (zero, false, nil) on duplicate, (zero, false, err) on failure.
func (s *IngestService) IngestUpload(
	ctx context.Context,
	fileBytes []byte,
	filename, fileType string,
	title, additionalContext string,
	linkedContexts []string,
) (model.Job, bool, error) {
	hash := ContentHash(fileBytes)

	_, isDuplicate, err := s.docs.GetByHash(ctx, hash)
	if err != nil {
		return model.Job{}, false, fmt.Errorf("check duplicate: %w", err)
	}
	if isDuplicate {
		slog.Info("duplicate hash — skipping upload", "hash_prefix", hash[:8])
		return model.Job{}, false, nil
	}

	now := time.Now().UTC()
	ingestMeta := IngestMeta{AttachmentFilename: filename}

	var pngPath *string
	var sourceArtifactID string

	switch fileType {
	case "png", "jpg", "jpeg":
		sourceArtifactID = uuid.NewString()
		artifactFilename := hash[:8] + ".png"
		if err := s.store.Save(s.vaultPath, sourceArtifactID, artifactFilename, fileBytes); err != nil {
			return model.Job{}, false, fmt.Errorf("save artifact: %w", err)
		}
		p := filepath.Join(s.vaultPath, "artifacts", sourceArtifactID, artifactFilename)
		pngPath = &p

	default: // txt, md
		rawText := string(fileBytes)
		ingestMeta.RawText = rawText
		ingestMeta.FileType = fileType
		if title == "" {
			title = titleFromText(filename, rawText)
		}
	}

	if title == "" {
		title = titleFromFilename(filename)
	}

	doc := model.Document{
		ID:                uuid.NewString(),
		ContentHash:       hash,
		PNGPath:           pngPath,
		Title:             strPtr(title),
		DateMonth:         strPtr(now.Format("2006-01")),
		AdditionalContext: additionalContext,
		LinkedContexts:    linkedContexts,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if err := s.docs.Insert(ctx, doc); err != nil {
		return model.Job{}, false, fmt.Errorf("insert document: %w", err)
	}

	if sourceArtifactID != "" {
		artifact := model.Artifact{
			ID:          sourceArtifactID,
			DocumentID:  doc.ID,
			Filename:    hash[:8] + ".png",
			ContentType: "image/png",
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := s.artifacts.Insert(ctx, artifact); err != nil {
			return model.Job{}, false, fmt.Errorf("insert artifact: %w", err)
		}
	}

	if err := s.storeIngestMeta(ctx, doc.ID, ingestMeta); err != nil {
		return model.Job{}, false, err
	}

	if err := s.events.Append(ctx, model.StageEvent{
		DocumentID: doc.ID,
		Stage:      "upload",
		EventType:  model.EventReceived,
		Timestamp:  now,
	}); err != nil {
		return model.Job{}, false, fmt.Errorf("append event: %w", err)
	}

	job, err := s.createFirstJob(ctx, doc, now)
	if err != nil {
		return model.Job{}, false, err
	}

	slog.Info("created document via upload", "doc_id", doc.ID[:8], "hash_prefix", hash[:8])
	return job, true, nil
}

func (s *IngestService) createFirstJob(ctx context.Context, doc model.Document, now time.Time) (model.Job, error) {
	if len(s.pipeline.Stages) == 0 {
		return model.Job{}, fmt.Errorf("pipeline has no stages")
	}
	first := s.pipeline.Stages[0]
	job := model.Job{
		ID:         uuid.NewString(),
		DocumentID: doc.ID,
		Stage:      first.Name,
		Status:     model.JobStatusPending,
		Options: model.JobOptions{
			RequireContext: first.RequireContext,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.jobs.Upsert(ctx, job); err != nil {
		return model.Job{}, fmt.Errorf("upsert job: %w", err)
	}
	return job, nil
}

func (s *IngestService) storeIngestMeta(ctx context.Context, docID string, meta IngestMeta) error {
	b, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal ingest meta: %w", err)
	}
	if err := s.kv.Set(ctx, "ingest_meta:"+docID, string(b)); err != nil {
		return fmt.Errorf("store ingest meta: %w", err)
	}
	return nil
}

func titleFromWebhookMeta(meta map[string]any, attachmentFilename string) *string {
	if dests, ok := meta["destinations"].([]any); ok {
		for _, d := range dests {
			if name := strings.TrimSpace(fmt.Sprint(d)); name != "" {
				return &name
			}
		}
	}
	if t := titleFromFilename(attachmentFilename); t != "" {
		return &t
	}
	return nil
}

func titleFromFilename(filename string) string {
	stem := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	if stem != "" && !genericFilenames[strings.ToLower(stem)] {
		return stem
	}
	return ""
}

func titleFromText(filename, rawText string) string {
	if t := titleFromFilename(filename); t != "" {
		return t
	}
	for _, line := range strings.Split(rawText, "\n") {
		line = strings.TrimLeft(line, "# ")
		line = strings.TrimSpace(line)
		if line != "" && len(line) <= 80 {
			return line
		}
	}
	return ""
}

func strPtr(s string) *string { return &s }
