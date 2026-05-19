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

	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"

	"github.com/fagerbergj/document-pipeline/server/core/adk"
	adktools "github.com/fagerbergj/document-pipeline/server/core/adk/tools"
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
	docs        port.DocumentRepo
	jobs        port.JobRepo
	artifacts   port.ArtifactRepo
	events      port.StageEventRepo
	contexts    port.ContextRepo
	kv          port.KeyValueRepo
	store       port.DocumentArtifactStore
	llm         port.LLMInference
	embed       port.EmbedStore
	transcriber port.Transcriber
	streams     port.StreamManager
	prompts     port.PromptRenderer
	sessionSvc  session.Service
	pipeline    model.PipelineConfig
	vaultPath   string
	embedModel  string
	ragTool     tool.Tool
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
	transcriber port.Transcriber,
	streams port.StreamManager,
	prompts port.PromptRenderer,
	sessionSvc session.Service,
	pipeline model.PipelineConfig,
	vaultPath string,
) *WorkerService {
	em := "nomic-embed-text:v1.5"
	for _, s := range pipeline.Stages {
		if s.Type == model.StageTypeEmbed && s.Model != "" {
			em = s.Model
			break
		}
	}
	ragTool, _ := adktools.NewRagSearchTool(embed, llm.GenerateEmbed, em, 0, 0)
	return &WorkerService{
		docs: docs, jobs: jobs, artifacts: artifacts, events: events,
		contexts: contexts, kv: kv, store: store, llm: llm, embed: embed,
		transcriber: transcriber,
		streams:     streams, prompts: prompts, sessionSvc: sessionSvc,
		pipeline: pipeline, vaultPath: vaultPath, embedModel: em, ragTool: ragTool,
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
			stage.Type != model.StageTypeEmbed &&
			stage.Type != model.StageTypeTranscribe {
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

		// llm-swap unloads idle models on its own TTL; no explicit unload call.

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
		processErr = w.runLLMText(ctx, doc, job, stage, ingestMeta, stageData)
	case model.StageTypeEmbed:
		processErr = w.runEmbed(ctx, doc, job, stage, stageData)
	case model.StageTypeTranscribe:
		processErr = w.runTranscribe(ctx, doc, job, stage, ingestMeta)
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

	// Skip path: this stage doesn't apply to the file type (e.g. OCR on audio,
	// or OCR on a plain-text upload). Two sub-paths:
	//   - text upload: persist the pre-extracted text as raw_text so downstream
	//     stages have an input.
	//   - other (audio): no-op skip — transcribe already produced raw_text.
	//     Persisting an empty run here would overwrite transcribe's good output
	//     in the stage data map (findInput iterates random order).
	if meta != nil && isSkipFileType(stage, meta.FileType) {
		rawText := meta.RawText
		if rawText != "" {
			outputField := "raw_text"
			if len(stage.Outputs) > 0 {
				outputField = stage.Outputs[0].Field
			}
			inputs := []fieldDraft{mdField("raw_text", rawText)}
			outputs := []fieldDraft{mdField(outputField, rawText)}

			title := ""
			if doc.Title != nil {
				title = *doc.Title
			}
			if title == "" {
				title = titleFromText(meta.AttachmentFilename, rawText)
			}

			run, err := w.persistRun(ctx, job, inputs, outputs, model.ConfidenceHigh, nil)
			if err != nil {
				return err
			}
			if err := w.jobs.UpdateRuns(ctx, job.ID, appendRun(job.Runs, run), now); err != nil {
				return err
			}
			if title != "" && (doc.Title == nil || *doc.Title == "") {
				doc.Title = &title
				doc.UpdatedAt = now
				_ = w.docs.Update(ctx, doc)
			}
		}
		_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusDone), now)
		_ = w.events.Append(ctx, model.StageEvent{DocumentID: doc.ID, Stage: stage.Name, EventType: model.EventSkipped, Timestamp: now})
		_ = w.advancePipeline(ctx, job, now)
		slog.Info("doc skipped OCR", "doc_id", doc.ID[:8], "stage", stage.Name, "file_type", meta.FileType, "wrote_raw_text", rawText != "")
		return nil
	}

	if doc.MediaPath == nil {
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

	imageBytes, err := os.ReadFile(*doc.MediaPath)
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

	outputField := "raw_text"
	if len(stage.Outputs) > 0 {
		outputField = stage.Outputs[0].Field
	}
	inputs := []fieldDraft{txtField("source", "(image)")}
	outputs := []fieldDraft{mdField(outputField, text)}

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
	run, err := w.persistRun(ctx, job, inputs, outputs, model.ConfidenceHigh, nil)
	if err != nil {
		return err
	}
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
	_ = w.advancePipeline(ctx, job, now)
	slog.Info("OCR done", "doc_id", doc.ID[:8], "stage", stage.Name)
	return nil
}

// --- Transcribe stage ---

func (w *WorkerService) runTranscribe(
	ctx context.Context, doc model.Document, job model.Job,
	stage model.StageDefinition, meta *IngestMeta,
) error {
	now := time.Now().UTC()

	// Skip non-audio inputs (image + text). Mirrors the OCR text-skip flow:
	// emit a no-op skipped event so downstream stages still advance.
	if meta == nil || !meta.FileType.IsTranscribable() || isSkipFileType(stage, meta.FileType) {
		_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusDone), now)
		_ = w.events.Append(ctx, model.StageEvent{DocumentID: doc.ID, Stage: stage.Name, EventType: model.EventSkipped, Timestamp: now})
		_ = w.advancePipeline(ctx, job, now)
		slog.Info("doc skipped transcribe (not audio)", "doc_id", doc.ID[:8], "stage", stage.Name)
		return nil
	}

	if doc.MediaPath == nil {
		return fmt.Errorf("no media path on document %s", doc.ID[:8])
	}

	// User-seeded transcript artifact: if the doc has an artifact tagged with
	// this stage's name and raw_text field, consume its content as the output
	// and skip whisper + the Ollama-unload churn.
	seed, hasSeed, err := w.artifacts.GetByStageField(ctx, doc.ID, stage.Name, "raw_text")
	if err != nil {
		return fmt.Errorf("lookup seed artifact: %w", err)
	}
	if hasSeed {
		text, err := readArtifactText(w.store, w.vaultPath, seed)
		if err != nil {
			return fmt.Errorf("read seed artifact: %w", err)
		}
		return w.finishTranscribe(ctx, doc, job, stage, meta, "(user-supplied transcript)", text)
	}

	if w.transcriber == nil {
		return fmt.Errorf("transcribe stage configured but no transcriber wired")
	}

	audioBytes, err := os.ReadFile(*doc.MediaPath)
	if err != nil {
		return fmt.Errorf("read audio: %w", err)
	}

	w.streams.Publish(job.ID, port.StreamEvent{Type: port.EventStatus, Data: `{"text":"Transcribing…"}`})

	// llm-swap evicts idle models on its own TTL, so whisper (ctranslate2,
	// separate process) gets the GPU back without us forcing an unload here.

	text, err := w.transcriber.Transcribe(ctx, stage.Model, audioBytes, filepathBase(*doc.MediaPath))
	if err != nil {
		return fmt.Errorf("transcribe: %w", err)
	}

	if wasStopped(ctx, w.jobs, job.ID) {
		return nil
	}

	return w.finishTranscribe(ctx, doc, job, stage, meta, "(audio)", text)
}

// finishTranscribe writes the transcribe run, applies the title fallback, marks
// the job done, and advances the pipeline. Shared by the whisper and
// user-supplied-transcript paths.
func (w *WorkerService) finishTranscribe(
	ctx context.Context, doc model.Document, job model.Job,
	stage model.StageDefinition, meta *IngestMeta,
	sourceLabel, text string,
) error {
	text = strings.TrimSpace(text)
	if text == "" {
		text = "(no speech recognised)"
	}

	outputField := "raw_text"
	if len(stage.Outputs) > 0 {
		outputField = stage.Outputs[0].Field
	}
	inputs := []fieldDraft{txtField("source", sourceLabel)}
	outputs := []fieldDraft{mdField(outputField, text)}

	now := time.Now().UTC()
	run, err := w.persistRun(ctx, job, inputs, outputs, model.ConfidenceHigh, nil)
	if err != nil {
		return err
	}
	if err := w.jobs.UpdateRuns(ctx, job.ID, appendRun(job.Runs, run), now); err != nil {
		return err
	}

	freshDoc, _ := w.docs.Get(ctx, doc.ID)
	if freshDoc.Title == nil || *freshDoc.Title == "" {
		var newTitle string
		switch {
		case doc.Title != nil && *doc.Title != "":
			newTitle = *doc.Title
		case meta != nil:
			newTitle = titleFromText(meta.AttachmentFilename, text)
		default:
			newTitle = titleFromText("", text)
		}
		if newTitle != "" {
			freshDoc.Title = &newTitle
			freshDoc.UpdatedAt = now
			_ = w.docs.Update(ctx, freshDoc)
		}
	}

	_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusDone), now)
	_ = w.events.Append(ctx, model.StageEvent{DocumentID: doc.ID, Stage: stage.Name, EventType: model.EventCompleted, Timestamp: now})
	_ = w.advancePipeline(ctx, job, now)
	slog.Info("transcribe done", "doc_id", doc.ID[:8], "stage", stage.Name, "source", sourceLabel, "chars", len(text))
	return nil
}

func filepathBase(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}

// --- LLM text stage ---

func (w *WorkerService) runLLMText(
	ctx context.Context, doc model.Document, job model.Job,
	stage model.StageDefinition, meta *IngestMeta, stageData map[string]map[string]any,
) error {
	now := time.Now().UTC()

	if !checkStartIf(doc, stage) {
		_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusWaiting), now)
		_ = w.events.Append(ctx, model.StageEvent{DocumentID: doc.ID, Stage: stage.Name, EventType: model.EventWaitingForContext, Timestamp: now})
		return nil
	}

	// File-type skip: e.g. clarify is configured to skip audio uploads since
	// transcribe already populates clarified_text. Without this check the stage
	// runs with vision:true and feeds raw audio bytes as an image to the LLM.
	if meta != nil && isSkipFileType(stage, meta.FileType) {
		_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusDone), now)
		_ = w.events.Append(ctx, model.StageEvent{DocumentID: doc.ID, Stage: stage.Name, EventType: model.EventSkipped, Timestamp: now})
		_ = w.advancePipeline(ctx, job, now)
		slog.Info("doc skipped LLM stage (file_type)", "doc_id", doc.ID[:8], "stage", stage.Name, "file_type", meta.FileType)
		return nil
	}

	inputText, inputField := findInput(stageData, stage.Input)

	// Don't burn an LLM call when the upstream stage produced no text. Mark the
	// job as error so the doc shows the failure plainly instead of churning out
	// a "I have no input to process" output that downstream stages then propagate.
	if stage.Input != "" && inputText == "" {
		return fmt.Errorf("no input text for field %q (upstream stage produced none)", stage.Input)
	}

	// input_size_lt_kb: skip the stage when its input is shorter than the
	// configured threshold (used by `summarize` for already-short documents).
	// When the stage has a single Output field configured, pass the input
	// through as that output so downstream stages still find the field they
	// expect (e.g. clarify reading `narrative_summary` works regardless of
	// whether summarize actually ran).
	if isSkipBySize(stage, inputText) {
		now = time.Now().UTC()
		if stage.Output != "" {
			inputs := []fieldDraft{txtField("source", "(passthrough — input below size threshold)")}
			outputs := []fieldDraft{mdField(stage.Output, inputText)}
			run, err := w.persistRun(ctx, job, inputs, outputs, model.ConfidenceHigh, nil)
			if err != nil {
				return err
			}
			if err := w.jobs.UpdateRuns(ctx, job.ID, appendRun(job.Runs, run), now); err != nil {
				return err
			}
		}
		_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusDone), now)
		_ = w.events.Append(ctx, model.StageEvent{DocumentID: doc.ID, Stage: stage.Name, EventType: model.EventSkipped, Timestamp: now})
		_ = w.advancePipeline(ctx, job, now)
		slog.Info("doc skipped (input below size threshold)", "doc_id", doc.ID[:8], "stage", stage.Name, "bytes", len(inputText), "passthrough", stage.Output != "")
		return nil
	}

	promptText := ""
	if stage.Prompt != "" {
		linkedContext, linkedContextName := w.loadLinkedContext(ctx, doc)
		data := map[string]any{
			"DocumentContext":   doc.AdditionalContext,
			"Context":           linkedContext,
			"LinkedContext":     linkedContext,
			"LinkedContextName": linkedContextName,
		}
		var err error
		promptText, err = w.prompts.Render(stage.Prompt, data)
		if err != nil {
			return fmt.Errorf("render LLM prompt: %w", err)
		}
	}

	// Only attach the source artifact as a vision input when it's actually an
	// image. Audio docs reach clarify too (transcripts benefit from cleanup),
	// but their .webm bytes would crash the LLM as image/png.
	var imageBytes []byte
	if stage.Vision && doc.MediaPath != nil && meta != nil && meta.FileType.IsImage() {
		b, err := os.ReadFile(*doc.MediaPath)
		if err != nil {
			slog.Warn("could not read image for vision stage", "err", err)
		} else {
			imageBytes = b
		}
	}

	w.streams.Publish(job.ID, port.StreamEvent{Type: port.EventStatus, Data: `{"text":"Loading model ` + stage.Model + `\u2026"}`})

	// The rendered prompt is the system instruction; the input text is the user message.
	// Keeping them in separate roles gives the model a clean boundary between
	// "what to do" (system) and "what to process" (user).
	//
	// On a re-run after the user answered clarifying questions, send the answers
	// as the user turn instead of the original input. The ADK session keyed by
	// job.ID retains the prior turns, so the model sees: input \u2192 questions \u2192
	// answers, and produces a final response. Without this, the user message
	// stays "the original raw text" forever, and the model has no signal that
	// its previous questions were addressed \u2014 so it asks them again.
	userMsgText := inputText
	if qa := buildQAFollowup(job.Runs); qa != "" {
		userMsgText = qa
	}
	userParts := []*genai.Part{{Text: userMsgText}}
	if imageBytes != nil {
		userParts = append(userParts, &genai.Part{
			InlineData: &genai.Blob{MIMEType: "image/png", Data: imageBytes},
		})
	}

	mdl := adk.NewPortLLMModel(w.llm, stage.Model)
	result, genErr := adk.RunAgent(ctx, mdl, []tool.Tool{w.ragTool}, promptText, userParts, w.sessionSvc, job.ID, func(ev adk.StreamEvent) {
		// Token events skip JSON-encoding for the live-log stream — that
		// consumer treats Data as the raw text, not a JSON envelope.
		if ev.Kind == adk.StreamEventToken {
			w.streams.Publish(job.ID, port.StreamEvent{Type: port.EventToken, Data: ev.Text})
			return
		}
		eventType := ev.SSEEventType()
		if eventType == "" {
			return
		}
		payload, err := ev.JSONPayload()
		if err != nil {
			return
		}
		w.streams.Publish(job.ID, port.StreamEvent{Type: eventType, Data: string(payload)})
	})
	if genErr != nil {
		w.streams.Publish(job.ID, port.StreamEvent{Type: port.EventDone})
		return fmt.Errorf("LLM generate: %w", genErr)
	}
	rawResp := result.Text

	if wasStopped(ctx, w.jobs, job.ID) {
		w.streams.Publish(job.ID, port.StreamEvent{Type: port.EventDone})
		return nil
	}

	inputs, outputs, confidence, questions := parseLLMResponse(rawResp, inputField, inputText, stage)
	if len(outputs) == 0 {
		slog.Warn("LLM response produced no outputs", "stage", stage.Name, "raw_len", len(rawResp), "raw_preview", truncate(rawResp, 200))
	}

	now = time.Now().UTC()
	run, err := w.persistRun(ctx, job, inputs, outputs, confidence, questions)
	if err != nil {
		w.streams.Publish(job.ID, port.StreamEvent{Type: port.EventDone})
		return err
	}
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
	w.streams.Publish(job.ID, port.StreamEvent{Type: port.EventDone})
	_ = w.advancePipeline(ctx, job, now)
	slog.Info("LLM text done", "doc_id", doc.ID[:8], "stage", stage.Name)
	return nil
}

// --- Embed stage ---

const defaultChunkSize = 1500
const defaultChunkOverlap = 200

// chunkText splits text into overlapping character-based chunks.
// Empty/whitespace-only fragments (which can appear at tail boundaries when
// the input ends in a run of whitespace) are dropped — downstream stores
// reject blank content, and embedding blanks is wasted work either way.
func chunkText(text string, size, overlap int) []string {
	keep := func(c string) bool { return strings.TrimSpace(c) != "" }
	if len(text) <= size {
		if !keep(text) {
			return nil
		}
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
		if c := text[i:end]; keep(c) {
			chunks = append(chunks, c)
		}
		if end == len(text) {
			break
		}
	}
	return chunks
}

// contextualizeChunk asks the embed stage's contextual LLM for a 1-2 sentence
// situating context that locates the chunk inside the document. The document
// is sent FIRST so any prompt-prefix caching the runtime supports can be
// reused across chunks. Returns the trimmed text or an error.
func (w *WorkerService) contextualizeChunk(ctx context.Context, stage model.StageDefinition, document, chunk string) (string, error) {
	prompt, err := w.prompts.Render(stage.ContextualPrompt, map[string]any{
		"Document": document,
		"Chunk":    chunk,
	})
	if err != nil {
		return "", fmt.Errorf("render contextual prompt: %w", err)
	}
	var buf strings.Builder
	if err := w.llm.GenerateText(ctx, stage.ContextualModel, prompt, func(c string) {
		buf.WriteString(c)
	}); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
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
	if job.Options.Embed != nil && job.Options.Embed.EmbedImage && doc.MediaPath != nil {
		b, err := os.ReadFile(*doc.MediaPath)
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

		chunkPayload := make(map[string]any, len(basePayload)+5)
		for k, v := range basePayload {
			chunkPayload[k] = v
		}
		chunkPayload[port.PayloadText] = chunk
		chunkPayload[port.PayloadChunkIndex] = i
		if i > 0 {
			chunkPayload[port.PayloadPrevChunk] = fmt.Sprintf("%s-%04d", doc.ID, i-1)
		}
		if i < len(chunks)-1 {
			chunkPayload[port.PayloadNextChunk] = fmt.Sprintf("%s-%04d", doc.ID, i+1)
		}

		// Contextual embeddings: ask the small LLM to produce a 1-2 sentence
		// situating context for this chunk, prepend it to the chunk before
		// embedding, and store the context separately on the payload so
		// retrieval results can show both. Failure falls back to raw-chunk
		// embedding rather than failing the whole stage.
		embedText := chunk
		if stage.ContextualModel != "" {
			ctxText, err := w.contextualizeChunk(ctx, stage, inputText, chunk)
			if err != nil {
				slog.Warn("contextual embed: falling back to raw chunk", "doc_id", doc.ID[:8], "chunk_index", i, "err", err)
			} else if ctxText != "" {
				chunkPayload[port.PayloadContext] = ctxText
				embedText = ctxText + "\n\n" + chunk
			}
		}

		vec, err := w.llm.GenerateEmbed(ctx, stage.Model, embedText)
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
	var inputs []fieldDraft
	if inputField != "" {
		inputs = []fieldDraft{mdField(inputField, inputText)}
	}
	outputs := []fieldDraft{txtField("chunks", fmt.Sprintf("%d", len(chunks)))}
	run, err := w.persistRun(ctx, job, inputs, outputs, model.ConfidenceHigh, nil)
	if err != nil {
		return err
	}
	if err := w.jobs.UpdateRuns(ctx, job.ID, appendRun(job.Runs, run), now); err != nil {
		return err
	}
	_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusDone), now)
	_ = w.events.Append(ctx, model.StageEvent{DocumentID: doc.ID, Stage: stage.Name, EventType: model.EventCompleted, Timestamp: now})
	_ = w.advancePipeline(ctx, job, now)
	slog.Info("embed done", "doc_id", doc.ID[:8], "stage", stage.Name, "chunks", len(chunks))
	return nil
}

// kvSeriesCorpusHashPrefix is the KV key prefix for the SHA256 of the last
// successfully-embedded corpus for a series. Used by rebuildSeriesCorpus to
// short-circuit when nothing has changed since the previous rebuild — without
// this guard, queueing embed on N docs in a series fires N identical rebuilds.
const kvSeriesCorpusHashPrefix = "series_corpus_hash:"

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

	// Skip the rebuild entirely if the corpus hash matches the last successful
	// rebuild. Embed-replay on every doc in a series would otherwise produce N
	// identical rebuilds, each hammering qdrant with the same chunks.
	corpusHash := ContentHash([]byte(combined))
	hashKey := kvSeriesCorpusHashPrefix + series
	if prev, ok, err := w.kv.Get(ctx, hashKey); err == nil && ok && prev == corpusHash {
		now := time.Now().UTC()
		_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusDone), now)
		_ = w.events.Append(ctx, model.StageEvent{DocumentID: doc.ID, Stage: stage.Name, EventType: model.EventSkipped, Timestamp: now})
		_ = w.advancePipeline(ctx, job, now)
		slog.Info("series corpus rebuild skipped — content unchanged", "series", series, "doc_id", doc.ID[:8])
		return nil
	}

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
			port.PayloadTitle:      series,
			port.PayloadText:       chunk,
			port.PayloadChunkIndex: i,
		}
		if i > 0 {
			payload[port.PayloadPrevChunk] = fmt.Sprintf("series:%s-%04d", series, i-1)
		}
		if i < len(chunks)-1 {
			payload[port.PayloadNextChunk] = fmt.Sprintf("series:%s-%04d", series, i+1)
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
	outputs := []fieldDraft{
		txtField("series_docs", fmt.Sprintf("%d", len(seriesDocs))),
		txtField("chunks", fmt.Sprintf("%d", len(chunks))),
	}
	run, err := w.persistRun(ctx, job, nil, outputs, model.ConfidenceHigh, nil)
	if err != nil {
		return err
	}
	if err := w.jobs.UpdateRuns(ctx, job.ID, appendRun(job.Runs, run), now); err != nil {
		return err
	}
	_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusDone), now)
	_ = w.events.Append(ctx, model.StageEvent{DocumentID: doc.ID, Stage: stage.Name, EventType: model.EventCompleted, Timestamp: now})
	_ = w.advancePipeline(ctx, job, now)
	// Persist the corpus hash so subsequent embed-replays on other docs in the
	// series can short-circuit when nothing has changed.
	_ = w.kv.Set(ctx, hashKey, corpusHash)
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
		errorRun, perr := w.persistRun(ctx, job, nil, []fieldDraft{txtField("error", jobErr.Error())}, model.ConfidenceLow, nil)
		if perr != nil {
			slog.Warn("could not persist error run", "err", perr)
		} else {
			_ = w.jobs.UpdateRuns(ctx, job.ID, appendRun(job.Runs, errorRun), now)
		}
		_ = w.jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusError), now)

		// Block any downstream pending jobs so they don't run on the stale or
		// empty input this stage failed to produce. Happens when the user
		// replays an earlier stage (which cascades downstream to pending) and
		// then that earlier stage fails — without this, every later stage would
		// run with whatever (possibly empty) artifacts are lying around.
		stageNames := make([]string, len(w.pipeline.Stages))
		for i, s := range w.pipeline.Stages {
			stageNames[i] = s.Name
		}
		if err := w.jobs.CascadeBlock(ctx, doc.ID, stage.Name, stageNames, now); err != nil {
			slog.Warn("failed to block downstream jobs after permanent failure", "doc_id", doc.ID[:8], "stage", stage.Name, "err", err)
		}
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
	return CollectStageData(ctx, w.jobs, w.artifacts, w.store, w.vaultPath, docID)
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

// isSkipBySize returns true when stage.SkipIf["input_size_lt_kb"] is set and
// the input text is smaller than the configured threshold (in KB).
func isSkipBySize(stage model.StageDefinition, inputText string) bool {
	if stage.SkipIf == nil {
		return false
	}
	v, ok := stage.SkipIf["input_size_lt_kb"]
	if !ok {
		return false
	}
	var kb int
	switch t := v.(type) {
	case int:
		kb = t
	case int64:
		kb = int(t)
	case float64:
		kb = int(t)
	default:
		return false
	}
	if kb <= 0 {
		return false
	}
	return len(inputText) < kb*1024
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
	// Skip empty values: for audio uploads _ingest.raw_text is "" while
	// transcribe.raw_text holds the real text, and Go map iteration is random
	// — without this we sometimes hit the empty one first and clarify gets no
	// input.
	for _, sd := range stageData {
		v, ok := sd[inputField]
		if !ok {
			continue
		}
		s := fmt.Sprint(v)
		if s == "" {
			continue
		}
		return s, inputField
	}
	return "", ""
}

// buildQAFollowup formats a follow-up user turn from the latest run's answered
// questions. Returns "" when there are no prior runs or none of their questions
// have answers — the caller falls back to sending the original input text.
//
// The session.Service holds the prior input + questions turn under the same
// job.ID, so this message only needs to convey the answers.
func buildQAFollowup(runs []model.Run) string {
	if len(runs) == 0 {
		return ""
	}
	latest := runs[len(runs)-1]
	var lines []string
	for _, q := range latest.Questions {
		if strings.TrimSpace(q.Answer) == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- Segment: %q\n  Question: %s\n  Answer: %s", q.Segment, q.Question, q.Answer))
	}
	if len(lines) == 0 {
		return ""
	}
	return "Here are my answers to your follow-up questions. Use them to produce a final clarified version — do not ask these again.\n\n" + strings.Join(lines, "\n")
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
// It returns in-memory drafts (text + intended file extension); the caller writes
// them to disk via persistRun.
func parseLLMResponse(raw, inputField, inputText string, stage model.StageDefinition) (
	inputs []fieldDraft, outputs []fieldDraft, confidence model.Confidence, questions []model.Question,
) {
	confidence = model.ConfidenceMedium
	if inputField != "" {
		inputs = []fieldDraft{mdField(inputField, inputText)}
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

		outputField := stage.Output
		if outputField == "" && len(stage.Outputs) > 0 {
			outputField = stage.Outputs[0].Field
		}
		if outputField != "" && clarified != "" {
			outputs = []fieldDraft{mdField(outputField, clarified)}
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
		// Model returned plain text (e.g. markdown) — use it directly as the output field.
		outputField := stage.Output
		if outputField == "" && len(stage.Outputs) > 0 {
			outputField = stage.Outputs[0].Field
		}
		if outputField != "" && strings.TrimSpace(raw) != "" {
			slog.Info("LLM returned plain text; using as output", "stage", stage.Name, "field", outputField)
			outputs = []fieldDraft{mdField(outputField, strings.TrimSpace(raw))}
		} else {
			slog.Warn("failed to parse LLM JSON response", "err", err)
		}
		return
	}

	if c, ok := parsed["confidence"].(string); ok {
		confidence = model.Confidence(c)
	}

	if stage.Output != "" {
		if v, ok := parsed[stage.Output]; ok {
			outputs = append(outputs, mdField(stage.Output, fmt.Sprint(v)))
		}
	}
	for _, o := range stage.Outputs {
		if v, ok := parsed[o.Field]; ok {
			text := fmt.Sprint(v)
			ext := "md"
			if b, err := json.Marshal(v); err == nil && (o.Type == "json_array" || o.Type == "json") {
				text = string(b)
				ext = "json"
			}
			outputs = append(outputs, fieldDraft{field: o.Field, text: text, ext: ext})
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
