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

// kvIngestMetaPrefix is the KeyValueRepo key prefix used to store IngestMeta per document.
const kvIngestMetaPrefix = "ingest_meta:"

var genericFilenames = map[string]bool{
	"remarkable": true,
	"untitled":   true,
	"image":      true,
	"attachment": true,
	"document":   true,
}

// IngestRequest carries all inputs needed to ingest a file.
// The HTTP layer is responsible for populating this from the request.
type IngestRequest struct {
	FileBytes         []byte
	Filename          string
	FileType          model.FileType
	Title             string
	AdditionalContext string
	LinkedContexts    []string
	Meta              IngestMeta // stored in kv for worker use
}

// IngestMeta is arbitrary metadata stored in kv for the worker to read at processing time.
type IngestMeta struct {
	Meta               map[string]any `json:"meta,omitempty"`
	AttachmentFilename string         `json:"attachment_filename,omitempty"`
	RawText            string         `json:"raw_text,omitempty"`
	FileType           model.FileType `json:"file_type,omitempty"`
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

// Ingest hashes the file, checks for duplicates, saves the artifact, and creates the
// document and first pipeline job. Returns (job, true, nil) on success,
// (zero, false, nil) on duplicate, or (zero, false, err) on failure.
func (s *IngestService) Ingest(ctx context.Context, req IngestRequest) (model.Job, bool, error) {
	hash := ContentHash(req.FileBytes)

	_, isDuplicate, err := s.docs.GetByHash(ctx, hash)
	if err != nil {
		return model.Job{}, false, fmt.Errorf("check duplicate: %w", err)
	}
	if isDuplicate {
		slog.Info("duplicate hash — skipping ingest", "hash_prefix", hash[:8])
		return model.Job{}, false, nil
	}

	now := time.Now().UTC()
	meta := req.Meta

	var pngPath *string
	var sourceArtifactID string

	switch req.FileType {
	case model.FileTypePNG, model.FileTypeJPG, model.FileTypeJPEG:
		sourceArtifactID = uuid.NewString()
		artifactFilename := hash[:8] + ".png"
		if err := s.store.Save(s.vaultPath, sourceArtifactID, artifactFilename, req.FileBytes); err != nil {
			return model.Job{}, false, fmt.Errorf("save artifact: %w", err)
		}
		p := filepath.Join(s.vaultPath, "artifacts", sourceArtifactID, artifactFilename)
		pngPath = &p

	default: // txt, md
		rawText := string(req.FileBytes)
		meta.RawText = rawText
		meta.FileType = req.FileType
		if req.Title == "" {
			req.Title = titleFromText(req.Filename, rawText)
		}
	}

	title := req.Title
	if title == "" {
		title = titleFromFilename(req.Filename)
	}

	doc := model.Document{
		ID:                uuid.NewString(),
		ContentHash:       hash,
		PNGPath:           pngPath,
		AdditionalContext: req.AdditionalContext,
		LinkedContexts:    req.LinkedContexts,
		DateMonth:         strPtr(now.Format("2006-01")),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if title != "" {
		doc.Title = &title
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

	b, err := json.Marshal(meta)
	if err != nil {
		return model.Job{}, false, fmt.Errorf("marshal ingest meta: %w", err)
	}
	if err := s.kv.Set(ctx, kvIngestMetaPrefix+doc.ID, string(b)); err != nil {
		return model.Job{}, false, fmt.Errorf("store ingest meta: %w", err)
	}

	if err := s.events.Append(ctx, model.StageEvent{
		DocumentID: doc.ID,
		Stage:      "ingest",
		EventType:  model.EventReceived,
		Timestamp:  now,
	}); err != nil {
		return model.Job{}, false, fmt.Errorf("append event: %w", err)
	}

	job, err := s.createFirstJob(ctx, doc, now)
	if err != nil {
		return model.Job{}, false, err
	}

	slog.Info("document ingested", "doc_id", doc.ID[:8], "hash_prefix", hash[:8])
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
