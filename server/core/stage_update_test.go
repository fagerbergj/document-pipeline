package core

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"google.golang.org/adk/session"

	"github.com/fagerbergj/document-pipeline/server/core/model"
)

func TestUpdateStageArtifact_HappyPath(t *testing.T) {
	deps, jobs, _, vault, jobID, artifactRel := setupStageUpdateFixture(t)

	stage, downstream, err := UpdateStageArtifact(context.Background(), deps, "doc-1", model.FieldClarifiedText, "REWRITTEN")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if stage != model.StageNameClarify {
		t.Errorf("stage: want clarify, got %q", stage)
	}
	if got := downstream; len(got) != 2 || got[0] != model.StageNameClassify || got[1] != model.StageNameEmbed {
		t.Errorf("downstream: %+v", got)
	}

	// Artifact on disk reflects the new content.
	data, err := os.ReadFile(vault + "/" + artifactRel)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if string(data) != "REWRITTEN" {
		t.Errorf("artifact bytes: %q", string(data))
	}

	// The edited stage stays done (re-pending it would regenerate and discard
	// the edit); only downstream re-runs.
	if jobs.statuses[jobID] != string(model.JobStatusDone) {
		t.Errorf("edited stage status = %q, want done", jobs.statuses[jobID])
	}
	if jobs.statuses["job-classify-1"] != string(model.JobStatusPending) {
		t.Errorf("downstream classify status = %q, want pending", jobs.statuses["job-classify-1"])
	}

	// Run preview + size refreshed.
	gotJob := jobs.jobs[jobID]
	out := gotJob.Runs[0].Outputs[0]
	if out.Size != int64(len("REWRITTEN")) {
		t.Errorf("size: %d", out.Size)
	}
	if out.Preview == "" {
		t.Errorf("preview not refreshed")
	}
}

func TestUpdateStageArtifact_UnknownField(t *testing.T) {
	deps, _, _, _, _, _ := setupStageUpdateFixture(t)
	_, _, err := UpdateStageArtifact(context.Background(), deps, "doc-1", "nonexistent_field", "x")
	if !errors.Is(err, ErrUnknownField) {
		t.Errorf("want ErrUnknownField, got %v", err)
	}
}

func TestUpdateStageArtifact_StageNotDone(t *testing.T) {
	deps, jobs, _, _, jobID, _ := setupStageUpdateFixture(t)
	// Flip the job to running so the check fires.
	j := jobs.jobs[jobID]
	j.Status = model.JobStatusRunning
	jobs.jobs[jobID] = j

	_, _, err := UpdateStageArtifact(context.Background(), deps, "doc-1", model.FieldClarifiedText, "x")
	if !errors.Is(err, ErrStageNotDone) {
		t.Errorf("want ErrStageNotDone, got %v", err)
	}
}

func TestCurrentStageOutput_ReadsCurrentArtifact(t *testing.T) {
	deps, _, _, _, _, _ := setupStageUpdateFixture(t)
	current, stage, err := CurrentStageOutput(context.Background(), deps, "doc-1", model.FieldClarifiedText)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if stage != model.StageNameClarify {
		t.Errorf("stage: %q", stage)
	}
	if current != "ORIGINAL CLARIFIED TEXT" {
		t.Errorf("content: %q", current)
	}
}

// resolveFieldStage must handle the singular `output:` form (summarize,
// clarify) as well as the list `outputs:` form. Production pipeline.yaml uses
// `output: clarified_text` for clarify, so without this the document body is
// uneditable — the bug this guards against.
func TestResolveFieldStage_HandlesSingularOutput(t *testing.T) {
	p := canonicalPipeline()
	if stage, err := resolveFieldStage(p, model.FieldClarifiedText); err != nil || stage != model.StageNameClarify {
		t.Errorf("clarified_text (singular output): stage=%q err=%v", stage, err)
	}
	if stage, err := resolveFieldStage(p, model.FieldNarrativeSummary); err != nil || stage != model.StageNameSummarize {
		t.Errorf("narrative_summary (singular output): stage=%q err=%v", stage, err)
	}
	if stage, err := resolveFieldStage(p, model.FieldSummary); err != nil || stage != model.StageNameClassify {
		t.Errorf("summary (list outputs): stage=%q err=%v", stage, err)
	}
}

func TestResolveCanonicalBody_PrefersClarifiedText(t *testing.T) {
	jobs := newMockJobRepo(
		doneBodyJob(model.StageNameOCR, model.FieldRawText),
		doneBodyJob(model.StageNameSummarize, model.FieldNarrativeSummary),
		doneBodyJob(model.StageNameClarify, model.FieldClarifiedText),
	)
	deps := StageUpdateDeps{Jobs: jobs, Pipeline: canonicalPipeline()}
	field, stage, err := ResolveCanonicalBody(context.Background(), deps, "doc-1")
	if err != nil || field != model.FieldClarifiedText || stage != model.StageNameClarify {
		t.Fatalf("field=%q stage=%q err=%v", field, stage, err)
	}
}

func TestResolveCanonicalBody_FallsBackWhenClarifySkipped(t *testing.T) {
	// A skipped stage is done with no output run.
	skipped := func(stage string) model.Job {
		return model.Job{ID: "job-" + stage, DocumentID: "doc-1", Stage: stage, Status: model.JobStatusDone}
	}
	jobs := newMockJobRepo(
		doneBodyJob(model.StageNameOCR, model.FieldRawText),
		doneBodyJob(model.StageNameSummarize, model.FieldNarrativeSummary),
		skipped(model.StageNameClarify),
	)
	deps := StageUpdateDeps{Jobs: jobs, Pipeline: canonicalPipeline()}
	field, stage, err := ResolveCanonicalBody(context.Background(), deps, "doc-1")
	if err != nil || field != model.FieldNarrativeSummary || stage != model.StageNameSummarize {
		t.Fatalf("field=%q stage=%q err=%v", field, stage, err)
	}
}

// An OCR-only doc with summarize+clarify skipped falls back to raw_text — and
// the resolver must match ocr's job, not transcribe (both declare raw_text).
func TestResolveCanonicalBody_FallsBackToRawTextFromOCR(t *testing.T) {
	skipped := func(stage string) model.Job {
		return model.Job{ID: "job-" + stage, DocumentID: "doc-1", Stage: stage, Status: model.JobStatusDone}
	}
	jobs := newMockJobRepo(
		doneBodyJob(model.StageNameOCR, model.FieldRawText),
		skipped(model.StageNameSummarize),
		skipped(model.StageNameClarify),
	)
	deps := StageUpdateDeps{Jobs: jobs, Pipeline: canonicalPipeline()}
	field, stage, err := ResolveCanonicalBody(context.Background(), deps, "doc-1")
	if err != nil || field != model.FieldRawText || stage != model.StageNameOCR {
		t.Fatalf("field=%q stage=%q err=%v", field, stage, err)
	}
}

func TestResolveCanonicalBody_NoneAvailable(t *testing.T) {
	deps := StageUpdateDeps{Jobs: newMockJobRepo(), Pipeline: canonicalPipeline()}
	if _, _, err := ResolveCanonicalBody(context.Background(), deps, "doc-1"); !errors.Is(err, ErrNoCanonicalBody) {
		t.Errorf("want ErrNoCanonicalBody, got %v", err)
	}
}

// ResolveCanonicalBody must NOT silently fall back to an upstream draft when the
// body stage is merely still running — that would edit the wrong artifact and be
// overwritten when the stage finishes. The caller should surface "wait".
func TestResolveCanonicalBody_SurfacesStageNotDone(t *testing.T) {
	running := model.Job{ID: "job-clarify", DocumentID: "doc-1", Stage: model.StageNameClarify, Status: model.JobStatusRunning}
	jobs := newMockJobRepo(
		doneBodyJob(model.StageNameOCR, model.FieldRawText),
		doneBodyJob(model.StageNameSummarize, model.FieldNarrativeSummary),
		running,
	)
	deps := StageUpdateDeps{Jobs: jobs, Pipeline: canonicalPipeline()}
	if _, _, err := ResolveCanonicalBody(context.Background(), deps, "doc-1"); !errors.Is(err, ErrStageNotDone) {
		t.Fatalf("want ErrStageNotDone (don't silently edit an upstream draft), got %v", err)
	}
}

func TestUpdateStageArtifactAt_WritesResolvedBody(t *testing.T) {
	deps, jobs, _, vault, jobID, artifactRel := setupStageUpdateFixture(t)
	field, stage, err := ResolveCanonicalBody(context.Background(), deps, "doc-1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	downstream, err := UpdateStageArtifactAt(context.Background(), deps, "doc-1", stage, field, "REWRITTEN")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if stage != model.StageNameClarify {
		t.Errorf("stage: want clarify, got %q", stage)
	}
	if len(downstream) != 2 {
		t.Errorf("downstream: %+v", downstream)
	}
	if data, _ := os.ReadFile(vault + "/" + artifactRel); string(data) != "REWRITTEN" {
		t.Errorf("artifact bytes: %q", string(data))
	}
	// The edited stage must STAY done: re-pending clarify would make the worker
	// regenerate clarified_text from its input and overwrite the user's edit.
	if jobs.statuses[jobID] != string(model.JobStatusDone) {
		t.Errorf("edited stage status = %q, want done (re-pending it discards the edit)", jobs.statuses[jobID])
	}
	// Downstream stages re-run so they consume the edited body.
	if jobs.statuses["job-classify-1"] != string(model.JobStatusPending) {
		t.Errorf("downstream classify status = %q, want pending", jobs.statuses["job-classify-1"])
	}
}

// Regression for the OCR-body bug: when the canonical body is raw_text from ocr
// (clarify+summarize skipped, transcribe skipped), the read/update must target
// ocr — not transcribe, the first stage that declares raw_text. Before the fix
// CurrentStageOutput re-resolved raw_text→transcribe and failed.
func TestCanonicalBody_OCRRawText_ReadAndUpdate(t *testing.T) {
	ctx := context.Background()
	vault := t.TempDir()
	store := &mockArtifactStore{}
	const docID = "doc-ocr"
	artRel := "runs/job-ocr/run-1/raw_text.md"
	if err := store.SaveAt(vault, artRel, []byte("OCR RAW TEXT")); err != nil {
		t.Fatal(err)
	}
	artifacts := &mockArtifactRepo{}
	artPath := artRel
	_ = artifacts.Insert(ctx, model.Artifact{ID: "art-ocr", DocumentID: docID, Filename: "raw_text.md", Path: &artPath})
	skipped := func(stage string) model.Job {
		return model.Job{ID: "job-" + stage, DocumentID: docID, Stage: stage, Status: model.JobStatusDone}
	}
	ocrJob := model.Job{
		ID: "job-ocr", DocumentID: docID, Stage: model.StageNameOCR, Status: model.JobStatusDone,
		Runs: []model.Run{{ID: "run-1", Outputs: []model.Field{{Field: model.FieldRawText, ArtifactID: "art-ocr"}}}},
	}
	jobs := newMockJobRepo(skipped(model.StageNameTranscribe), ocrJob, skipped(model.StageNameSummarize), skipped(model.StageNameClarify))
	deps := StageUpdateDeps{Jobs: jobs, Artifacts: artifacts, Store: store, SessionSvc: session.InMemoryService(), Pipeline: canonicalPipeline(), VaultPath: vault}

	current, stage, field, err := CurrentCanonicalBody(ctx, deps, docID)
	if err != nil {
		t.Fatalf("read canonical body: %v", err)
	}
	if current != "OCR RAW TEXT" || stage != model.StageNameOCR || field != model.FieldRawText {
		t.Fatalf("read got current=%q stage=%q field=%q", current, stage, field)
	}
	if _, err := UpdateStageArtifactAt(ctx, deps, docID, stage, field, "FIXED OCR"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if data, _ := os.ReadFile(vault + "/" + artRel); string(data) != "FIXED OCR" {
		t.Errorf("artifact bytes: %q", string(data))
	}
}

// canonicalPipeline mirrors production: summarize/clarify use the singular
// `output:` form, transcribe/ocr/classify use the `outputs:` list.
func canonicalPipeline() model.PipelineConfig {
	return model.PipelineConfig{Stages: []model.StageDefinition{
		{Name: model.StageNameTranscribe, Outputs: []model.StageOutput{{Field: model.FieldRawText}}},
		{Name: model.StageNameOCR, Outputs: []model.StageOutput{{Field: model.FieldRawText}}},
		{Name: model.StageNameSummarize, Output: model.FieldNarrativeSummary},
		{Name: model.StageNameClarify, Output: model.FieldClarifiedText},
		{Name: model.StageNameClassify, Outputs: []model.StageOutput{{Field: model.FieldTags}, {Field: model.FieldSummary}}},
		{Name: model.StageNameEmbed},
	}}
}

// doneBodyJob builds a completed job whose latest run emits the given field with
// a backing artifact id (enough for ResolveCanonicalBody / findDoneStageRun).
func doneBodyJob(stage, field string) model.Job {
	return model.Job{
		ID: "job-" + stage, DocumentID: "doc-1", Stage: stage, Status: model.JobStatusDone,
		Runs: []model.Run{{ID: "run-" + stage, Outputs: []model.Field{{Field: field, ArtifactID: "art-" + stage}}}},
	}
}

// setupStageUpdateFixture wires a minimal repo + store + pipeline so the
// stage_update helpers have something to read from and write to. Returns the
// deps, jobs repo (for assertions), filesystem store, vault path, and the
// pre-created job + relative artifact path.
func setupStageUpdateFixture(t *testing.T) (StageUpdateDeps, *mockJobRepo, *mockArtifactStore, string, string, string) {
	t.Helper()
	vault := t.TempDir()
	store := &mockArtifactStore{}

	const docID = "doc-1"
	const jobID = "job-clarify-1"
	const artifactID = "art-1"
	artifactRel := "runs/job-clarify-1/run-1/clarified_text.md"
	if err := store.SaveAt(vault, artifactRel, []byte("ORIGINAL CLARIFIED TEXT")); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	artifacts := &mockArtifactRepo{}
	artifactPath := artifactRel
	_ = artifacts.Insert(context.Background(), model.Artifact{
		ID:         artifactID,
		DocumentID: docID,
		Filename:   "clarified_text.md",
		Path:       &artifactPath,
	})

	job := model.Job{
		ID:         jobID,
		DocumentID: docID,
		Stage:      model.StageNameClarify,
		Status:     model.JobStatusDone,
		Runs: []model.Run{{
			ID: "run-1",
			Outputs: []model.Field{{
				Field:      model.FieldClarifiedText,
				ArtifactID: artifactID,
				Size:       int64(len("ORIGINAL CLARIFIED TEXT")),
				Preview:    "ORIGINAL",
			}},
			UpdatedAt: time.Now().Add(-1 * time.Hour),
		}},
	}
	// A downstream stage so a cascade has something to re-pend.
	classifyJob := model.Job{
		ID: "job-classify-1", DocumentID: docID, Stage: model.StageNameClassify, Status: model.JobStatusDone,
	}
	jobs := newMockJobRepo(job, classifyJob)

	pipeline := model.PipelineConfig{
		Stages: []model.StageDefinition{
			{Name: model.StageNameClarify, Outputs: []model.StageOutput{{Field: model.FieldClarifiedText}}},
			{Name: model.StageNameClassify, Outputs: []model.StageOutput{{Field: model.FieldTags}, {Field: model.FieldSummary}}},
			{Name: model.StageNameEmbed},
		},
	}

	deps := StageUpdateDeps{
		Jobs:       jobs,
		Artifacts:  artifacts,
		Store:      store,
		SessionSvc: session.InMemoryService(),
		Pipeline:   pipeline,
		VaultPath:  vault,
	}
	return deps, jobs, store, vault, jobID, artifactRel
}
