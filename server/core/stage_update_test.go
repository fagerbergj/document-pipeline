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

	// Job has been re-pended.
	if jobs.statuses[jobID] != string(model.JobStatusPending) {
		t.Errorf("job status after update: %q, want pending", jobs.statuses[jobID])
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
	jobs := newMockJobRepo(job)

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
