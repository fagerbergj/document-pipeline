package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
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

// IngestRequest carries all inputs needed to ingest a buffered file.
// The HTTP layer is responsible for populating this from the request.
type IngestRequest struct {
	FileBytes         []byte
	Filename          string
	FileType          model.FileType
	Title             string
	AdditionalContext string
	LinkedContexts    []string
	Series            string
	Meta              IngestMeta // stored in kv for worker use
}

// IngestStreamedRequest carries the inputs for a streamed ingest, where the
// caller has already saved the bytes to a temp file under <vault>/tmp/. The
// IngestStreamed flow hashes during a single io.Copy pass into the artifacts
// dir, avoiding any in-memory buffering of the full file.
type IngestStreamedRequest struct {
	TempFilePath      string
	Filename          string
	FileType          model.FileType
	Title             string
	AdditionalContext string
	LinkedContexts    []string
	Series            string
	Meta              IngestMeta
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

// Ingest hashes the buffered bytes, checks for duplicates, saves the artifact,
// and creates the document and first pipeline job. Returns (job, true, nil) on
// success, (zero, false, nil) on duplicate, or (zero, false, err) on failure.
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

	var mediaPath *string
	var sourceArtifactID, sourceArtifactFilename, sourceContentType string

	switch {
	case req.FileType.IsImage():
		sourceArtifactID = uuid.NewString()
		sourceArtifactFilename = hash[:8] + "." + string(req.FileType)
		sourceContentType = mimeForFileType(req.FileType)
		if err := s.store.Save(s.vaultPath, sourceArtifactID, sourceArtifactFilename, req.FileBytes); err != nil {
			return model.Job{}, false, fmt.Errorf("save artifact: %w", err)
		}
		p := filepath.Join(s.vaultPath, "artifacts", sourceArtifactID, sourceArtifactFilename)
		mediaPath = &p

	case req.FileType.IsAudio():
		sourceArtifactID = uuid.NewString()
		sourceArtifactFilename = hash[:8] + "." + string(req.FileType)
		sourceContentType = mimeForFileType(req.FileType)
		if err := s.store.Save(s.vaultPath, sourceArtifactID, sourceArtifactFilename, req.FileBytes); err != nil {
			return model.Job{}, false, fmt.Errorf("save audio artifact: %w", err)
		}
		p := filepath.Join(s.vaultPath, "artifacts", sourceArtifactID, sourceArtifactFilename)
		mediaPath = &p

	default: // txt, md
		rawText := string(req.FileBytes)
		meta.RawText = rawText
		meta.FileType = req.FileType
		if req.Title == "" {
			req.Title = titleFromText(req.Filename, rawText)
		}
	}

	return s.finalize(ctx, finalizeArgs{
		Hash:              hash,
		MediaPath:         mediaPath,
		SourceArtifactID:  sourceArtifactID,
		SourceFilename:    sourceArtifactFilename,
		SourceContentType: sourceContentType,
		Title:             req.Title,
		AdditionalContext: req.AdditionalContext,
		LinkedContexts:    req.LinkedContexts,
		Series:            req.Series,
		Meta:              meta,
		Now:               now,
	})
}

// IngestStreamed handles uploads where the bytes have already been saved to a
// temp file (used by the streaming POST /documents/stream endpoint and by the
// multipart endpoint, which spills uploads >32MB to disk via mime/multipart).
// Hashes during a single io.Copy pass into the final artifact location, then
// finalizes via the shared helper.
func (s *IngestService) IngestStreamed(ctx context.Context, req IngestStreamedRequest) (model.Job, bool, error) {
	src, err := os.Open(req.TempFilePath)
	if err != nil {
		return model.Job{}, false, fmt.Errorf("open temp file: %w", err)
	}
	defer src.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, src); err != nil {
		return model.Job{}, false, fmt.Errorf("hash temp file: %w", err)
	}
	hash := hex.EncodeToString(hasher.Sum(nil))

	_, isDuplicate, err := s.docs.GetByHash(ctx, hash)
	if err != nil {
		return model.Job{}, false, fmt.Errorf("check duplicate: %w", err)
	}
	if isDuplicate {
		slog.Info("duplicate hash — skipping streamed ingest", "hash_prefix", hash[:8])
		_ = os.Remove(req.TempFilePath)
		return model.Job{}, false, nil
	}

	now := time.Now().UTC()
	meta := req.Meta
	if meta.FileType == "" {
		meta.FileType = req.FileType
	}

	sourceArtifactID := uuid.NewString()
	ext := string(req.FileType)
	sourceArtifactFilename := hash[:8] + "." + ext
	sourceContentType := mimeForFileType(req.FileType)

	dstDir := filepath.Join(s.vaultPath, "artifacts", sourceArtifactID)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return model.Job{}, false, fmt.Errorf("mkdir artifact dir: %w", err)
	}
	dstPath := filepath.Join(dstDir, sourceArtifactFilename)
	if err := os.Rename(req.TempFilePath, dstPath); err != nil {
		// Cross-FS fallback: copy then remove.
		if err := copyFile(req.TempFilePath, dstPath); err != nil {
			return model.Job{}, false, fmt.Errorf("move temp -> artifact: %w", err)
		}
		_ = os.Remove(req.TempFilePath)
	}
	mediaPath := dstPath

	if req.FileType.IsText() {
		// Text files also store raw_text on the kv meta so OCR's text-skip path
		// can find it without re-reading the artifact.
		b, err := os.ReadFile(dstPath)
		if err == nil {
			meta.RawText = string(b)
		}
		if req.Title == "" {
			req.Title = titleFromText(req.Filename, meta.RawText)
		}
	}

	return s.finalize(ctx, finalizeArgs{
		Hash:              hash,
		MediaPath:         &mediaPath,
		SourceArtifactID:  sourceArtifactID,
		SourceFilename:    sourceArtifactFilename,
		SourceContentType: sourceContentType,
		Title:             req.Title,
		AdditionalContext: req.AdditionalContext,
		LinkedContexts:    req.LinkedContexts,
		Series:            req.Series,
		Meta:              meta,
		Now:               now,
	})
}

// finalizeArgs is the shared payload for the per-ingest tail (document insert,
// source-artifact insert, kv meta, first job creation).
type finalizeArgs struct {
	Hash              string
	MediaPath         *string
	SourceArtifactID  string
	SourceFilename    string
	SourceContentType string
	Title             string
	AdditionalContext string
	LinkedContexts    []string
	Series            string
	Meta              IngestMeta
	Now               time.Time
}

func (s *IngestService) finalize(ctx context.Context, a finalizeArgs) (model.Job, bool, error) {
	title := a.Title
	if title == "" {
		title = titleFromFilename(a.Meta.AttachmentFilename)
	}

	doc := model.Document{
		ID:                uuid.NewString(),
		ContentHash:       a.Hash,
		MediaPath:         a.MediaPath,
		AdditionalContext: a.AdditionalContext,
		LinkedContexts:    a.LinkedContexts,
		Series:            strPtr(a.Series),
		DateMonth:         strPtr(a.Now.Format("2006-01")),
		CreatedAt:         a.Now,
		UpdatedAt:         a.Now,
	}
	if title != "" {
		doc.Title = &title
	}

	if err := s.docs.Insert(ctx, doc); err != nil {
		return model.Job{}, false, fmt.Errorf("insert document: %w", err)
	}

	if a.SourceArtifactID != "" {
		artifact := model.Artifact{
			ID:          a.SourceArtifactID,
			DocumentID:  doc.ID,
			Filename:    a.SourceFilename,
			ContentType: a.SourceContentType,
			CreatedAt:   a.Now,
			UpdatedAt:   a.Now,
		}
		if err := s.artifacts.Insert(ctx, artifact); err != nil {
			return model.Job{}, false, fmt.Errorf("insert artifact: %w", err)
		}
	}

	b, err := json.Marshal(a.Meta)
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
		Timestamp:  a.Now,
	}); err != nil {
		return model.Job{}, false, fmt.Errorf("append event: %w", err)
	}

	job, err := s.createFirstJob(ctx, doc, a.Now)
	if err != nil {
		return model.Job{}, false, err
	}

	slog.Info("document ingested", "doc_id", doc.ID[:8], "hash_prefix", a.Hash[:8])
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

func mimeForFileType(ft model.FileType) string {
	switch ft {
	case model.FileTypePNG:
		return "image/png"
	case model.FileTypeJPG, model.FileTypeJPEG:
		return "image/jpeg"
	case model.FileTypeTXT:
		return "text/plain"
	case model.FileTypeMD:
		return "text/markdown"
	case model.FileTypeWEBM:
		return "audio/webm"
	case model.FileTypeWAV:
		return "audio/wav"
	case model.FileTypeMP3:
		return "audio/mpeg"
	case model.FileTypeM4A:
		return "audio/mp4"
	case model.FileTypeOGG:
		return "audio/ogg"
	case model.FileTypeFLAC:
		return "audio/flac"
	}
	return "application/octet-stream"
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
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
