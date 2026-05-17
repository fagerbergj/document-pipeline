package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/adk/session"

	"github.com/fagerbergj/document-pipeline/server/core/adk"
	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// Errors returned by the stage-artifact helpers. Surfaced through the
// update_document chat tool so the agent can describe failures to the user.
var (
	ErrUnknownField = errors.New("no pipeline stage produces this field")
	ErrNoJob        = errors.New("document has no job for this stage")
	ErrStageNotDone = errors.New("stage is not in 'done' status")
	ErrNoArtifact   = errors.New("stage output has no backing artifact")
)

// StageUpdateDeps bundles the repos + storage needed to read or rewrite a
// stage's output artifact. Constructed from the REST handler's existing
// dependencies; no new wiring.
type StageUpdateDeps struct {
	Jobs       port.JobRepo
	Artifacts  port.ArtifactRepo
	Store      port.DocumentArtifactStore
	SessionSvc session.Service
	Pipeline   model.PipelineConfig
	VaultPath  string
}

// CurrentStageOutput returns the current text of the output `field` for the
// document, resolving which stage produces that field via the pipeline config.
// Used by the chat tool to render a "before" preview alongside the proposed
// "after" content in the approval card.
func CurrentStageOutput(ctx context.Context, d StageUpdateDeps, docID, field string) (string, string, error) {
	stageName, err := resolveFieldStage(d.Pipeline, field)
	if err != nil {
		return "", "", err
	}
	_, run, fieldIdx, err := findDoneStageRun(ctx, d, docID, stageName, field)
	if err != nil {
		return "", stageName, err
	}
	art, err := d.Artifacts.Get(ctx, docID, run.Outputs[fieldIdx].ArtifactID)
	if err != nil {
		return "", stageName, fmt.Errorf("get artifact: %w", err)
	}
	text, err := readArtifactText(d.Store, d.VaultPath, art)
	if err != nil {
		return "", stageName, fmt.Errorf("read artifact: %w", err)
	}
	return text, stageName, nil
}

// UpdateStageArtifact overwrites the named output field of the doc's job for
// the corresponding stage, then re-pends the job so CascadeReplay invalidates
// every downstream stage. Returns the stage name and the list of downstream
// stage names that were re-queued.
//
// Mirrors the body of patchRun + putJobStatus in server/api/rest/jobs.go but
// keyed by (docID, field) instead of (jobID, runID, field) — the chat agent
// doesn't know run IDs.
func UpdateStageArtifact(ctx context.Context, d StageUpdateDeps, docID, field, content string) (string, []string, error) {
	stageName, err := resolveFieldStage(d.Pipeline, field)
	if err != nil {
		return "", nil, err
	}
	job, run, fieldIdx, err := findDoneStageRun(ctx, d, docID, stageName, field)
	if err != nil {
		return stageName, nil, err
	}

	// Rewrite the artifact bytes on disk.
	fld := run.Outputs[fieldIdx]
	art, err := d.Artifacts.Get(ctx, docID, fld.ArtifactID)
	if err != nil {
		return stageName, nil, fmt.Errorf("get artifact: %w", err)
	}
	data := []byte(content)
	if art.Path != nil && *art.Path != "" {
		if err := d.Store.SaveAt(d.VaultPath, *art.Path, data); err != nil {
			return stageName, nil, fmt.Errorf("save artifact: %w", err)
		}
	} else {
		if err := d.Store.Save(d.VaultPath, art.ID, art.Filename, data); err != nil {
			return stageName, nil, fmt.Errorf("save artifact: %w", err)
		}
	}

	// Refresh size + preview on the run, persist.
	now := time.Now().UTC()
	runIdx := -1
	for i, r := range job.Runs {
		if r.ID == run.ID {
			runIdx = i
			break
		}
	}
	job.Runs[runIdx].Outputs[fieldIdx].Size = int64(len(data))
	job.Runs[runIdx].Outputs[fieldIdx].Preview = PreviewOf(content)
	job.Runs[runIdx].UpdatedAt = now
	if err := d.Jobs.UpdateRuns(ctx, job.ID, job.Runs, now); err != nil {
		return stageName, nil, fmt.Errorf("update runs: %w", err)
	}

	// Re-pend the job and cascade to downstream stages.
	if err := d.Jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusPending), now); err != nil {
		return stageName, nil, fmt.Errorf("update status: %w", err)
	}
	stageOrder := make([]string, len(d.Pipeline.Stages))
	for i, s := range d.Pipeline.Stages {
		stageOrder[i] = s.Name
	}
	// Mirror putJobStatus: delete ADK sessions for every job on this doc so
	// any in-flight clarify/classify sessions don't replay stale context.
	if allJobs, err := d.Jobs.ListForDocument(ctx, docID); err == nil {
		for _, j := range allJobs {
			adk.DeleteSession(ctx, d.SessionSvc, j.ID)
		}
	}
	if err := d.Jobs.CascadeReplay(ctx, docID, stageName, stageOrder, now); err != nil {
		return stageName, nil, fmt.Errorf("cascade replay: %w", err)
	}
	downstream := downstreamStages(stageOrder, stageName)
	slog.Info("stage artifact updated via chat tool",
		"doc_id", ShortID(docID), "stage", stageName, "field", field,
		"bytes", len(data), "downstream", downstream)
	return stageName, downstream, nil
}

// resolveFieldStage looks up which stage in the configured pipeline produces
// the named output field. Returns ErrUnknownField if no stage matches.
func resolveFieldStage(p model.PipelineConfig, field string) (string, error) {
	for _, s := range p.Stages {
		for _, o := range s.Outputs {
			if o.Field == field {
				return s.Name, nil
			}
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownField, field)
}

// findDoneStageRun returns the doc's job for stageName along with its latest
// run and the index of the requested field within run.Outputs. Returns
// ErrNoJob / ErrStageNotDone / ErrNoArtifact as appropriate.
func findDoneStageRun(ctx context.Context, d StageUpdateDeps, docID, stageName, field string) (*model.Job, *model.Run, int, error) {
	jobs, err := d.Jobs.ListForDocument(ctx, docID)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("list jobs: %w", err)
	}
	for i := range jobs {
		j := &jobs[i]
		if j.Stage != stageName {
			continue
		}
		if j.Status != model.JobStatusDone {
			return nil, nil, 0, fmt.Errorf("%w: %s is %s", ErrStageNotDone, stageName, j.Status)
		}
		if len(j.Runs) == 0 {
			return nil, nil, 0, fmt.Errorf("%w: %s has no runs", ErrNoArtifact, stageName)
		}
		run := &j.Runs[len(j.Runs)-1]
		for k, o := range run.Outputs {
			if o.Field == field {
				if o.ArtifactID == "" {
					return nil, nil, 0, fmt.Errorf("%w: field %s on stage %s has no artifact_id", ErrNoArtifact, field, stageName)
				}
				return j, run, k, nil
			}
		}
		return nil, nil, 0, fmt.Errorf("%w: field %s not present on stage %s", ErrNoArtifact, field, stageName)
	}
	return nil, nil, 0, fmt.Errorf("%w: %s", ErrNoJob, stageName)
}

// ShortID returns the first 8 chars of id for logging, or the full string if
// shorter. Avoids out-of-bounds panics on stub/test IDs that may be under
// 8 chars.
func ShortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// downstreamStages returns the stages that follow `stage` in the configured
// pipeline order, in the same order CascadeReplay re-pends them.
func downstreamStages(order []string, stage string) []string {
	for i, name := range order {
		if name == stage {
			out := make([]string, 0, len(order)-i-1)
			out = append(out, order[i+1:]...)
			return out
		}
	}
	return nil
}
