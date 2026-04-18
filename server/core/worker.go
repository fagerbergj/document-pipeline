package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

var confidenceLevels = map[model.Confidence]int{
	model.ConfidenceLow:    0,
	model.ConfidenceMedium: 1,
	model.ConfidenceHigh:   2,
}

// WorkerService runs the pipeline stage loop.
type WorkerService struct {
	docs      port.DocumentRepo
	jobs      port.JobRepo
	artifacts port.ArtifactRepo
	events    port.StageEventRepo
	contexts  port.ContextRepo
	kv        port.KeyValueRepo
	store     port.DocumentArtifactStore
	llm       port.LLMInference
	embed     port.EmbedStore
	streams   port.StreamManager
	prompts   port.PromptRenderer
	pipeline  model.PipelineConfig
	vaultPath string
}

func NewWorkerService(
	docs port.DocumentRepo,
	jobs port.JobRepo,
	artifacts port.ArtifactRepo,
	events port.StageEventRepo,
	contexts port.ContextRepo,
	kv port.KeyValueRepo,
	store port.DocumentArtifactStore,
	llm port.LLMInference,
	embed port.EmbedStore,
	streams port.StreamManager,
	prompts port.PromptRenderer,
	pipeline model.PipelineConfig,
	vaultPath string,
) *WorkerService {
	return &WorkerService{
		docs, jobs, artifacts, events, contexts, kv, store,
		llm, embed, streams, prompts, pipeline, vaultPath,
	}
}

// RunOnce processes one pass through all pending stages and returns.
// It does not sleep between stages and does not reset running jobs.
// Intended for use in tests and one-shot invocations.
func (w *WorkerService) RunOnce(ctx context.Context) error {
	_, err := w.runOnce(ctx)
	return err
}

// Run starts the worker loop. It blocks until ctx is cancelled.
func (w *WorkerService) Run(ctx context.Context) error {
	slog.Info("worker started")

	if _, err := w.jobs.ResetRunning(ctx); err != nil {
		return fmt.Errorf("reset running jobs: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			slog.Info("worker shutting down")
			return nil
		default:
		}

		processedAny, err := w.runOnce(ctx)
		if err != nil {
			slog.Error("worker loop error", "err", err)
		}
		if !processedAny {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(5 * time.Second):
			}
		}
	}
}

// runOnce processes one stage worth of pending jobs. Returns true if any jobs ran.
func (w *WorkerService) runOnce(ctx context.Context) (bool, error) {
	for _, stage := range w.pipeline.Stages {
		if stage.Type != model.StageTypeComputerVision &&
			stage.Type != model.StageTypeLLMText &&
			stage.Type != model.StageTypeEmbed {
			continue
		}

		jobs, err := w.jobs.ListPending(ctx, stage.Name)
		if err != nil {
			return false, fmt.Errorf("list pending jobs for %s: %w", stage.Name, err)
		}
		if len(jobs) == 0 {
			continue
		}

		slog.Info("processing stage", "stage", stage.Name, "count", len(jobs))

		maxConcurrent := int64(w.pipeline.MaxConcurrent)
		if stage.MaxConcurrent != nil {
			maxConcurrent = int64(*stage.MaxConcurrent)
		}
		sem := semaphore.NewWeighted(maxConcurrent)

		eg, egCtx := errgroup.WithContext(ctx)
		for _, job := range jobs {
			job := job
			eg.Go(func() error {
				if err := sem.Acquire(egCtx, 1); err != nil {
					return nil // context cancelled
				}
				defer sem.Release(1)
				w.processJob(egCtx, job, stage)
				return nil
			})
		}
		_ = eg.Wait()

		if stage.Model != "" {
			if err := w.llm.Unload(ctx, stage.Model); err != nil {
				slog.Warn("failed to unload model", "model", stage.Model, "err", err)
			}
		}

		return true, nil // restart from earliest stage each iteration
	}

	return false, nil
}

func (w *WorkerService) processJob(ctx context.Context, job model.Job, stage model.StageDefinition) {
	now := time.Now().UTC()

	doc, err := w.docs.Get(ctx, job.DocumentID)
	if err != nil {
		slog.Error("job document not found", "job_id", job.ID[:8], "doc_id", job.DocumentID[:8])
		return
	}

	if err := w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusRunning), now); err != nil {
		slog.Error("failed to mark job running", "job_id", job.ID[:8], "err", err)
		return
	}
	_ = w.events.Append(ctx, model.StageEvent{
		DocumentID: doc.ID,
		Stage:      stage.Name,
		EventType:  model.EventStarted,
		Timestamp:  now,
	})

	ingestMeta, err := w.loadIngestMeta(ctx, doc.ID)
	if err != nil {
		slog.Warn("could not load ingest meta", "doc_id", doc.ID[:8], "err", err)
	}
	stageData, err := w.collectStageData(ctx, doc.ID)
	if err != nil {
		slog.Warn("could not collect stage data", "doc_id", doc.ID[:8], "err", err)
	}
	if ingestMeta != nil {
		stageData["_ingest"] = map[string]any{
			"raw_text":  ingestMeta.RawText,
			"file_type": ingestMeta.FileType,
			"meta":      ingestMeta.Meta,
		}
	}

	var processErr error
	switch stage.Type {
	case model.StageTypeComputerVision:
		processErr = w.runOCR(ctx, doc, job, stage, ingestMeta, stageData)
	case model.StageTypeLLMText:
		processErr = w.runLLMText(ctx, doc, job, stage, stageData)
	case model.StageTypeEmbed:
		processErr = w.runEmbed(ctx, doc, job, stage, stageData)
	}

	if processErr != nil {
		w.handleJobError(ctx, doc, job, stage, processErr)
	}
}

// --- OCR stage ---

func (w *WorkerService) runOCR(
	ctx context.Context, doc model.Document, job model.Job,
	stage model.StageDefinition, meta *IngestMeta, stageData map[string]map[string]any,
) error {
	now := time.Now().UTC()

	// Text file skip path
	if meta != nil && isSkipFileType(stage, meta.FileType) {
		rawText := meta.RawText
		outputField := "ocr_raw"
		if len(stage.Outputs) > 0 {
			outputField = stage.Outputs[0].Field
		}
		inputs := []model.Field{{Field: "raw_text", Text: rawText}}
		outputs := []model.Field{{Field: outputField, Text: rawText}}

		title := ""
		if doc.Title != nil {
			title = *doc.Title
		}
		if title == "" && meta != nil {
			title = titleFromText(meta.AttachmentFilename, rawText)
		}

		run := makeRun(inputs, outputs, model.ConfidenceHigh, nil, model.Suggestions{})
		if err := w.jobs.UpdateRuns(ctx, job.ID, appendRun(job.Runs, run), now); err != nil {
			return err
		}
		if title != "" && (doc.Title == nil || *doc.Title == "") {
			doc.Title = &title
			doc.UpdatedAt = now
			_ = w.docs.Update(ctx, doc)
		}
		_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusDone), now)
		_ = w.events.Append(ctx, model.StageEvent{DocumentID: doc.ID, Stage: stage.Name, EventType: model.EventSkipped, Timestamp: now})
		_ = w.advancePipeline(ctx, job, now)
		slog.Info("doc skipped OCR (text upload)", "doc_id", doc.ID[:8], "stage", stage.Name)
		return nil
	}

	if doc.PNGPath == nil {
		return fmt.Errorf("no PNG path on document %s", doc.ID[:8])
	}

	promptText := ""
	if stage.Prompt != "" {
		data := OCRPromptData{DocumentContext: doc.AdditionalContext}
		var err error
		promptText, err = w.prompts.Render(stage.Prompt, data)
		if err != nil {
			return fmt.Errorf("render OCR prompt: %w", err)
		}
	}

	imageBytes, err := os.ReadFile(*doc.PNGPath)
	if err != nil {
		return fmt.Errorf("read image: %w", err)
	}

	w.streams.Publish(job.ID, port.StreamEvent{Type: port.EventStatus, Data: `{"text":"Loading model ` + stage.Model + `\u2026"}`})
	var ocrText strings.Builder
	if err := w.llm.GenerateVision(ctx, stage.Model, promptText, imageBytes, func(chunk string) {
		ocrText.WriteString(chunk)
	}); err != nil {
		return fmt.Errorf("OCR vision: %w", err)
	}

	if wasStopped(ctx, w.jobs, job.ID) {
		return nil
	}

	text := ocrText.String()
	if text == "" {
		text = "(no text recognised)"
	}
	slog.Info("OCR complete", "doc_id", doc.ID[:8], "chars", len(text))

	outputField := "ocr_raw"
	if len(stage.Outputs) > 0 {
		outputField = stage.Outputs[0].Field
	}
	inputs := []model.Field{{Text: "(image)"}}
	outputs := []model.Field{{Field: outputField, Text: text}}

	// Re-fetch doc in case title was set while OCR was running
	freshDoc, _ := w.docs.Get(ctx, doc.ID)
	title := ""
	if freshDoc.Title != nil && *freshDoc.Title != "" {
		title = *freshDoc.Title
	} else if doc.Title != nil {
		title = *doc.Title
	} else if meta != nil {
		title = titleFromText(meta.AttachmentFilename, text)
	} else {
		title = titleFromText("", text)
	}

	now = time.Now().UTC()
	run := makeRun(inputs, outputs, model.ConfidenceHigh, nil, model.Suggestions{})
	if err := w.jobs.UpdateRuns(ctx, job.ID, appendRun(job.Runs, run), now); err != nil {
		return err
	}
	if title != "" && (freshDoc.Title == nil || *freshDoc.Title == "") {
		freshDoc.Title = &title
		freshDoc.UpdatedAt = now
		_ = w.docs.Update(ctx, freshDoc)
	}
	_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusDone), now)
	_ = w.events.Append(ctx, model.StageEvent{DocumentID: doc.ID, Stage: stage.Name, EventType: model.EventCompleted, Timestamp: now})
	_ = w.saveArtifacts(ctx, stage, outputs, job, now)
	_ = w.advancePipeline(ctx, job, now)
	slog.Info("OCR done", "doc_id", doc.ID[:8], "stage", stage.Name)
	return nil
}

// --- LLM text stage ---

func (w *WorkerService) runLLMText(
	ctx context.Context, doc model.Document, job model.Job,
	stage model.StageDefinition, stageData map[string]map[string]any,
) error {
	now := time.Now().UTC()

	if !checkStartIf(doc, stage) {
		_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusWaiting), now)
		_ = w.events.Append(ctx, model.StageEvent{DocumentID: doc.ID, Stage: stage.Name, EventType: model.EventWaitingForContext, Timestamp: now})
		return nil
	}

	inputText, inputField := findInput(stageData, stage.Input)

	promptText := ""
	if stage.Prompt != "" {
		linkedContext, linkedContextName := w.loadLinkedContext(ctx, doc)
		qaHistory := buildQAHistory(job.Runs)
		previousOutput := ""
		if len(job.Runs) > 0 {
			last := job.Runs[len(job.Runs)-1]
			if len(last.Outputs) > 0 {
				previousOutput = last.Outputs[0].Text
			}
		}
		data := map[string]any{
			"DocumentContext":   doc.AdditionalContext,
			"Context":           linkedContext,
			"LinkedContext":     linkedContext,
			"LinkedContextName": linkedContextName,
			"QAHistory":         qaHistory,
			"PreviousOutput":    previousOutput,
		}
		var err error
		promptText, err = w.prompts.Render(stage.Prompt, data)
		if err != nil {
			return fmt.Errorf("render LLM prompt: %w", err)
		}
	}

	var imageBytes []byte
	if stage.Vision && doc.PNGPath != nil {
		b, err := os.ReadFile(*doc.PNGPath)
		if err != nil {
			slog.Warn("could not read image for vision stage", "err", err)
		} else {
			imageBytes = b
		}
	}

	w.streams.Publish(job.ID, port.StreamEvent{Type: port.EventStatus, Data: `{"text":"Loading model ` + stage.Model + `\u2026"}`})
	var rawResp strings.Builder
	onChunk := func(chunk string) {
		rawResp.WriteString(chunk)
		w.streams.Publish(job.ID, port.StreamEvent{Type: port.EventToken, Data: chunk})
	}

	var genErr error
	if imageBytes != nil {
		genErr = w.llm.GenerateVision(ctx, stage.Model, promptText, imageBytes, onChunk)
	} else {
		genErr = w.llm.GenerateText(ctx, stage.Model, promptText+"\n\n"+inputText, onChunk)
	}
	if genErr != nil {
		w.streams.Publish(job.ID, port.StreamEvent{Type: port.EventDone})
		return fmt.Errorf("LLM generate: %w", genErr)
	}

	if wasStopped(ctx, w.jobs, job.ID) {
		w.streams.Publish(job.ID, port.StreamEvent{Type: port.EventDone})
		return nil
	}

	inputs, outputs, confidence, questions, suggestions := parseLLMResponse(rawResp.String(), inputField, inputText, stage)

	now = time.Now().UTC()
	run := makeRun(inputs, outputs, confidence, questions, suggestions)
	if err := w.jobs.UpdateRuns(ctx, job.ID, appendRun(job.Runs, run), now); err != nil {
		w.streams.Publish(job.ID, port.StreamEvent{Type: port.EventDone})
		return err
	}

	if !checkContinueIf(stage, confidence) || len(questions) > 0 {
		_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusWaiting), now)
		_ = w.events.Append(ctx, model.StageEvent{DocumentID: doc.ID, Stage: stage.Name, EventType: model.EventAwaitingReview, Timestamp: now})
		w.streams.Publish(job.ID, port.StreamEvent{Type: port.EventDone})
		return nil
	}

	_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusDone), now)
	_ = w.events.Append(ctx, model.StageEvent{DocumentID: doc.ID, Stage: stage.Name, EventType: model.EventCompleted, Timestamp: now})
	_ = w.saveArtifacts(ctx, stage, outputs, job, now)
	w.streams.Publish(job.ID, port.StreamEvent{Type: port.EventDone})
	_ = w.advancePipeline(ctx, job, now)
	slog.Info("LLM text done", "doc_id", doc.ID[:8], "stage", stage.Name)
	return nil
}

// --- Embed stage ---

const defaultChunkSize = 1500
const defaultChunkOverlap = 200

// chunkText splits text into overlapping character-based chunks.
func chunkText(text string, size, overlap int) []string {
	if len(text) <= size {
		return []string{text}
	}
	step := size - overlap
	if step <= 0 {
		step = size
	}
	var chunks []string
	for i := 0; i < len(text); i += step {
		end := i + size
		if end > len(text) {
			end = len(text)
		}
		chunks = append(chunks, text[i:end])
		if end == len(text) {
			break
		}
	}
	return chunks
}

func (w *WorkerService) runEmbed(
	ctx context.Context, doc model.Document, job model.Job,
	stage model.StageDefinition, stageData map[string]map[string]any,
) error {
	if doc.Series != nil && *doc.Series != "" {
		return w.rebuildSeriesCorpus(ctx, doc, job, stage)
	}

	inputText, inputField := findInput(stageData, stage.Input)
	if inputText == "" {
		return fmt.Errorf("embed stage %q: no text found for input %q", stage.Name, stage.Input)
	}

	// Collect metadata from all stage outputs
	allData := map[string]any{}
	for _, sd := range stageData {
		for k, v := range sd {
			allData[k] = v
		}
	}
	basePayload := map[string]any{}
	for _, field := range stage.MetadataFields {
		if v, ok := allData[field]; ok {
			basePayload[field] = v
		}
	}
	if doc.Title != nil {
		basePayload[port.PayloadTitle] = *doc.Title
	}
	if doc.DateMonth != nil {
		basePayload[port.PayloadDateMonth] = *doc.DateMonth
	}
	basePayload[port.PayloadDocID] = doc.ID

	chunkSize := stage.ChunkSize
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}
	chunkOverlap := stage.ChunkOverlap
	if chunkOverlap <= 0 {
		chunkOverlap = defaultChunkOverlap
	}
	chunks := chunkText(inputText, chunkSize, chunkOverlap)

	// Load image bytes once (used for chunk 0 only).
	var imgBytes []byte
	if job.Options.Embed != nil && job.Options.Embed.EmbedImage && doc.PNGPath != nil {
		b, err := os.ReadFile(*doc.PNGPath)
		if err != nil {
			slog.Warn("could not read image for embed", "err", err)
		} else {
			imgBytes = b
		}
	}

	// Delete any existing chunks for this document before upserting new ones.
	if err := w.embed.DeleteByDocID(ctx, doc.ID); err != nil {
		slog.Warn("embed delete before re-upsert failed (continuing)", "doc_id", doc.ID[:8], "err", err)
	}

	for i, chunk := range chunks {
		chunkID := fmt.Sprintf("%s-%04d", doc.ID, i)

		chunkPayload := make(map[string]any, len(basePayload)+3)
		for k, v := range basePayload {
			chunkPayload[k] = v
		}
		chunkPayload[port.PayloadText] = chunk
		chunkPayload[port.PayloadChunkIndex] = i

		vec, err := w.llm.GenerateEmbed(ctx, stage.Model, chunk)
		if err != nil {
			return fmt.Errorf("chunk %d embed: %w", i, err)
		}

		var imageVector []float32
		if i == 0 && len(imgBytes) > 0 {
			iv, err := w.llm.GenerateEmbed(ctx, stage.Model, string(imgBytes))
			if err != nil {
				slog.Warn("image embed failed", "doc_id", doc.ID[:8], "err", err)
			} else {
				imageVector = iv
			}
		}

		if err := w.embed.Upsert(ctx, chunkID, vec, imageVector, chunkPayload); err != nil {
			return fmt.Errorf("chunk %d upsert: %w", i, err)
		}
	}

	now := time.Now().UTC()
	inputs := []model.Field{}
	if inputField != "" {
		inputs = []model.Field{{Field: inputField, Text: inputText}}
	}
	run := makeRun(inputs, []model.Field{{Field: "chunks", Text: fmt.Sprintf("%d", len(chunks))}}, model.ConfidenceHigh, nil, model.Suggestions{})
	if err := w.jobs.UpdateRuns(ctx, job.ID, appendRun(job.Runs, run), now); err != nil {
		return err
	}
	_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusDone), now)
	_ = w.events.Append(ctx, model.StageEvent{DocumentID: doc.ID, Stage: stage.Name, EventType: model.EventCompleted, Timestamp: now})
	_ = w.advancePipeline(ctx, job, now)
	slog.Info("embed done", "doc_id", doc.ID[:8], "stage", stage.Name, "chunks", len(chunks))
	return nil
}

// rebuildSeriesCorpus concatenates all docs in the series, chunks, and re-embeds the corpus.
func (w *WorkerService) rebuildSeriesCorpus(
	ctx context.Context, doc model.Document, job model.Job, stage model.StageDefinition,
) error {
	series := *doc.Series

	seriesDocs, err := w.docs.ListBySeries(ctx, series)
	if err != nil {
		return fmt.Errorf("list series docs: %w", err)
	}

	// Gather text for each doc in the series.
	var parts []string
	for _, d := range seriesDocs {
		sd, err := w.collectStageData(ctx, d.ID)
		if err != nil {
			slog.Warn("could not collect stage data for series doc", "doc_id", d.ID[:8], "err", err)
			continue
		}
		text, _ := findInput(sd, stage.Input)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return fmt.Errorf("series %q: no text found across %d docs", series, len(seriesDocs))
	}
	combined := strings.Join(parts, "\n\n---\n\n")

	chunkSize := stage.ChunkSize
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}
	chunkOverlap := stage.ChunkOverlap
	if chunkOverlap <= 0 {
		chunkOverlap = defaultChunkOverlap
	}
	chunks := chunkText(combined, chunkSize, chunkOverlap)

	// Delete old series corpus before rebuilding.
	if err := w.embed.DeleteBySeries(ctx, series); err != nil {
		slog.Warn("series corpus delete failed (continuing)", "series", series, "err", err)
	}

	for i, chunk := range chunks {
		chunkID := fmt.Sprintf("series:%s-%04d", series, i)
		payload := map[string]any{
			port.PayloadSeriesName: series,
			port.PayloadText:       chunk,
			port.PayloadChunkIndex: i,
		}
		vec, err := w.llm.GenerateEmbed(ctx, stage.Model, chunk)
		if err != nil {
			return fmt.Errorf("series chunk %d embed: %w", i, err)
		}
		if err := w.embed.Upsert(ctx, chunkID, vec, nil, payload); err != nil {
			return fmt.Errorf("series chunk %d upsert: %w", i, err)
		}
	}

	now := time.Now().UTC()
	run := makeRun(nil, []model.Field{
		{Field: "series_docs", Text: fmt.Sprintf("%d", len(seriesDocs))},
		{Field: "chunks", Text: fmt.Sprintf("%d", len(chunks))},
	}, model.ConfidenceHigh, nil, model.Suggestions{})
	if err := w.jobs.UpdateRuns(ctx, job.ID, appendRun(job.Runs, run), now); err != nil {
		return err
	}
	_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusDone), now)
	_ = w.events.Append(ctx, model.StageEvent{DocumentID: doc.ID, Stage: stage.Name, EventType: model.EventCompleted, Timestamp: now})
	_ = w.advancePipeline(ctx, job, now)
	slog.Info("series corpus rebuilt", "series", series, "docs", len(seriesDocs), "chunks", len(chunks))
	return nil
}

// --- Error handling ---

func (w *WorkerService) handleJobError(ctx context.Context, doc model.Document, job model.Job, stage model.StageDefinition, jobErr error) {
	w.streams.Publish(job.ID, port.StreamEvent{Type: port.EventDone})
	slog.Error("stage failed", "stage", stage.Name, "doc_id", doc.ID[:8], "err", jobErr)

	now := time.Now().UTC()
	_ = w.events.Append(ctx, model.StageEvent{
		DocumentID: doc.ID,
		Stage:      stage.Name,
		EventType:  model.EventFailed,
		Timestamp:  now,
		Data:       map[string]any{port.EventFieldError: jobErr.Error()},
	})

	failures, _ := w.events.CountFailures(ctx, doc.ID, stage.Name)
	if failures < 3 {
		backoff := time.Duration(1<<failures) * time.Second
		slog.Info("will retry", "doc_id", doc.ID[:8], "stage", stage.Name, "backoff", backoff, "attempt", failures)
		time.Sleep(backoff)
		_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusPending), now)
	} else {
		slog.Error("exhausted retries", "doc_id", doc.ID[:8], "stage", stage.Name)
		errorRun := makeRun(nil, []model.Field{{Field: "error", Text: jobErr.Error()}}, model.ConfidenceLow, nil, model.Suggestions{})
		_ = w.jobs.UpdateRuns(ctx, job.ID, appendRun(job.Runs, errorRun), now)
		_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusError), now)
	}
}

// --- Pipeline advancement ---

func (w *WorkerService) advancePipeline(ctx context.Context, job model.Job, now time.Time) error {
	names := make([]string, len(w.pipeline.Stages))
	for i, s := range w.pipeline.Stages {
		names[i] = s.Name
	}
	idx := -1
	for i, name := range names {
		if name == job.Stage {
			idx = i
			break
		}
	}
	if idx < 0 || idx+1 >= len(names) {
		return nil
	}
	nextStage := names[idx+1]
	existing, found, err := w.jobs.GetByDocumentAndStage(ctx, job.DocumentID, nextStage)
	if err != nil {
		return err
	}
	if found {
		return w.jobs.UpdateStatus(ctx, existing.ID, string(model.JobStatusPending), now)
	}
	nextDef := w.pipeline.Stages[idx+1]
	nextJob := model.Job{
		ID:         uuid.NewString(),
		DocumentID: job.DocumentID,
		Stage:      nextStage,
		Status:     model.JobStatusPending,
		Options: model.JobOptions{
			RequireContext: nextDef.RequireContext,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	return w.jobs.Upsert(ctx, nextJob)
}

// --- Artifact saving ---

func (w *WorkerService) saveArtifacts(ctx context.Context, stage model.StageDefinition, outputs []model.Field, job model.Job, now time.Time) error {
	if !stage.SaveAsArtifact {
		return nil
	}
	for _, item := range outputs {
		if item.Text == "" {
			continue
		}
		field := item.Field
		if field == "" {
			field = stage.Name
		}
		filename := field + ".md"
		artifactID := uuid.NewString()
		if err := w.store.Save(w.vaultPath, artifactID, filename, []byte(item.Text)); err != nil {
			slog.Warn("failed to save artifact", "filename", filename, "err", err)
			continue
		}
		artifact := model.Artifact{
			ID:           artifactID,
			DocumentID:   job.DocumentID,
			Filename:     filename,
			ContentType:  "text/markdown",
			CreatedJobID: &job.ID,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := w.artifacts.Insert(ctx, artifact); err != nil {
			slog.Warn("failed to insert artifact record", "filename", filename, "err", err)
		}
	}
	return nil
}

// --- Helpers ---

func (w *WorkerService) loadIngestMeta(ctx context.Context, docID string) (*IngestMeta, error) {
	raw, ok, err := w.kv.Get(ctx, kvIngestMetaPrefix+docID)
	if err != nil || !ok {
		return nil, err
	}
	var meta IngestMeta
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func (w *WorkerService) collectStageData(ctx context.Context, docID string) (map[string]map[string]any, error) {
	jobs, err := w.jobs.ListForDocument(ctx, docID)
	if err != nil {
		return nil, err
	}
	stageData := map[string]map[string]any{}
	for _, j := range jobs {
		if (j.Status == model.JobStatusDone || j.Status == model.JobStatusWaiting) && len(j.Runs) > 0 {
			latest := j.Runs[len(j.Runs)-1]
			outputs := map[string]any{}
			for _, f := range latest.Outputs {
				if f.Field != "" {
					outputs[f.Field] = f.Text
				}
			}
			if len(outputs) > 0 {
				stageData[j.Stage] = outputs
			}
		}
	}
	return stageData, nil
}

func (w *WorkerService) loadLinkedContext(ctx context.Context, doc model.Document) (string, string) {
	if len(doc.LinkedContexts) == 0 {
		return "", ""
	}
	entry, err := w.contexts.List(ctx)
	if err != nil {
		return "", ""
	}
	for _, c := range entry {
		for _, id := range doc.LinkedContexts {
			if c.ID == id {
				return c.Text, c.Name
			}
		}
	}
	return "", ""
}

func checkStartIf(doc model.Document, stage model.StageDefinition) bool {
	rules := stage.StartIf
	if stage.RequireContext || (rules != nil && rules["context_provided"] != nil) {
		if doc.AdditionalContext == "" && len(doc.LinkedContexts) == 0 {
			return false
		}
	}
	return true
}

func checkContinueIf(stage model.StageDefinition, confidence model.Confidence) bool {
	if stage.ContinueIf == nil {
		return true
	}
	actual := confidenceLevels[confidence]
	for _, rule := range stage.ContinueIf {
		if req, ok := rule["confidence"].(string); ok {
			if actual >= confidenceLevels[model.Confidence(req)] {
				return true
			}
		}
	}
	return false
}

func isSkipFileType(stage model.StageDefinition, fileType model.FileType) bool {
	if stage.SkipIf == nil {
		return false
	}
	types, ok := stage.SkipIf["file_type"].([]any)
	if !ok {
		return false
	}
	for _, t := range types {
		if model.FileType(fmt.Sprint(t)) == fileType {
			return true
		}
	}
	return false
}

func findInput(stageData map[string]map[string]any, inputField string) (text, field string) {
	if inputField == "" {
		return "", ""
	}
	for _, sd := range stageData {
		if v, ok := sd[inputField]; ok {
			return fmt.Sprint(v), inputField
		}
	}
	return "", ""
}

func buildQAHistory(runs []model.Run) []QARound {
	var history []QARound
	for _, run := range runs {
		var responses []QAResponse
		for _, q := range run.Questions {
			if q.Answer != "" {
				responses = append(responses, QAResponse{Segment: q.Segment, Answer: q.Answer})
			}
		}
		if len(responses) > 0 {
			history = append(history, QARound{Responses: responses})
		}
	}
	return history
}

func makeRun(inputs []model.Field, outputs []model.Field, confidence model.Confidence, questions []model.Question, suggestions model.Suggestions) model.Run {
	now := time.Now().UTC()
	if questions == nil {
		questions = []model.Question{}
	}
	return model.Run{
		ID:          uuid.NewString(),
		Inputs:      inputs,
		Outputs:     outputs,
		Confidence:  confidence,
		Questions:   questions,
		Suggestions: suggestions,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func appendRun(runs []model.Run, run model.Run) []model.Run {
	return append(runs, run)
}

func wasStopped(ctx context.Context, jobs port.JobRepo, jobID string) bool {
	job, err := jobs.GetByID(ctx, jobID)
	if err != nil {
		return false
	}
	if job.Status != model.JobStatusRunning {
		slog.Info("job stopped externally — discarding result", "job_id", jobID[:8])
		return true
	}
	return false
}

// parseLLMResponse handles both the <clarified_text> XML format and the JSON format.
func parseLLMResponse(raw, inputField, inputText string, stage model.StageDefinition) (
	inputs []model.Field, outputs []model.Field, confidence model.Confidence, questions []model.Question, suggestions model.Suggestions,
) {
	confidence = model.ConfidenceMedium
	if inputField != "" {
		inputs = []model.Field{{Field: inputField, Text: inputText}}
	}

	if strings.Contains(raw, "<clarified_text>") {
		clarified := extractXMLTag(raw, "clarified_text")
		clarified = stripHTMLComments(clarified)
		conf := extractXMLTag(raw, "confidence")
		if conf != "" {
			confidence = model.Confidence(conf)
		}

		rawQ := extractXMLTag(raw, "questions")
		var parsedQ []struct {
			Segment  string `json:"segment"`
			Question string `json:"question"`
		}
		_ = json.Unmarshal([]byte(rawQ), &parsedQ)
		for _, q := range parsedQ {
			questions = append(questions, model.Question{Segment: q.Segment, Question: q.Question})
		}

		docCtxUpdate := extractXMLTag(raw, "document_context_update")
		linkedCtxUpdate := extractXMLTag(raw, "linked_context_update")
		suggestions = model.Suggestions{
			AdditionalContext: nullIfGeneric(docCtxUpdate),
			LinkedContext:     nullIfGeneric(linkedCtxUpdate),
		}

		outputField := stage.Output
		if outputField == "" && len(stage.Outputs) > 0 {
			outputField = stage.Outputs[0].Field
		}
		if outputField != "" && clarified != "" {
			outputs = []model.Field{{Field: outputField, Text: clarified}}
		}
		return
	}

	// JSON format
	cleaned := strings.TrimPrefix(strings.TrimSpace(raw), "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		slog.Warn("failed to parse LLM JSON response", "err", err)
		return
	}

	if c, ok := parsed["confidence"].(string); ok {
		confidence = model.Confidence(c)
	}

	if stage.Output != "" {
		if v, ok := parsed[stage.Output]; ok {
			outputs = append(outputs, model.Field{Field: stage.Output, Text: fmt.Sprint(v)})
		}
	}
	for _, o := range stage.Outputs {
		if v, ok := parsed[o.Field]; ok {
			text := fmt.Sprint(v)
			if b, err := json.Marshal(v); err == nil && (o.Type == "json_array" || o.Type == "json") {
				text = string(b)
			}
			outputs = append(outputs, model.Field{Field: o.Field, Text: text})
		}
	}
	return
}

var htmlCommentRe = regexp.MustCompile(`(?s)<!--.*?-->`)

func extractXMLTag(s, tag string) string {
	re := regexp.MustCompile(`(?s)<` + tag + `>(.*?)</` + tag + `>`)
	m := re.FindStringSubmatch(s)
	if m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func stripHTMLComments(s string) string {
	return strings.TrimSpace(htmlCommentRe.ReplaceAllString(s, ""))
}

var nullVals = map[string]bool{
	"none": true, "null": true, "n/a": true, "nothing": true,
	"no updates": true, "no new information": true, "": true,
}

func nullIfGeneric(s string) string {
	if nullVals[strings.ToLower(strings.TrimSpace(s))] {
		return ""
	}
	return s
}
