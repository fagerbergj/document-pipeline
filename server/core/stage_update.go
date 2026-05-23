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
	ErrUnknownField    = errors.New("no pipeline stage produces this field")
	ErrNoJob           = errors.New("document has no job for this stage")
	ErrStageNotDone    = errors.New("stage is not in 'done' status")
	ErrNoArtifact      = errors.New("stage output has no backing artifact")
	ErrNoCanonicalBody = errors.New("document has no completed body stage to edit")
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
	text, err := currentStageOutputAt(ctx, d, docID, stageName, field)
	return text, stageName, err
}

// currentStageOutputAt reads the current text of (stageName, field) without
// re-deriving the stage from the field. Callers that already know the stage
// (e.g. CurrentCanonicalBody, where raw_text may come from ocr rather than the
// first stage that declares it) pass it explicitly so the read targets the
// stage that actually ran.
func currentStageOutputAt(ctx context.Context, d StageUpdateDeps, docID, stageName, field string) (string, error) {
	_, run, fieldIdx, err := findDoneStageRun(ctx, d, docID, stageName, field)
	if err != nil {
		return "", err
	}
	art, err := d.Artifacts.Get(ctx, docID, run.Outputs[fieldIdx].ArtifactID)
	if err != nil {
		return "", fmt.Errorf("get artifact: %w", err)
	}
	text, err := readArtifactText(d.Store, d.VaultPath, art)
	if err != nil {
		return "", fmt.Errorf("read artifact: %w", err)
	}
	return text, nil
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
	downstream, err := UpdateStageArtifactAt(ctx, d, docID, stageName, field, content)
	return stageName, downstream, err
}

// UpdateStageArtifactAt overwrites (stageName, field)'s artifact and cascades to
// downstream stages, returning the downstream stage names. Callers pass the
// stage explicitly so a field declared by more than one stage (raw_text from
// transcribe or ocr) targets the one that actually produced this doc's output —
// resolveFieldStage would pick the first declarer, which may have been skipped.
func UpdateStageArtifactAt(ctx context.Context, d StageUpdateDeps, docID, stageName, field, content string) ([]string, error) {
	job, run, fieldIdx, err := findDoneStageRun(ctx, d, docID, stageName, field)
	if err != nil {
		return nil, err
	}

	// Rewrite the artifact bytes on disk.
	fld := run.Outputs[fieldIdx]
	art, err := d.Artifacts.Get(ctx, docID, fld.ArtifactID)
	if err != nil {
		return nil, fmt.Errorf("get artifact: %w", err)
	}
	data := []byte(content)
	if art.Path != nil && *art.Path != "" {
		if err := d.Store.SaveAt(d.VaultPath, *art.Path, data); err != nil {
			return nil, fmt.Errorf("save artifact: %w", err)
		}
	} else {
		if err := d.Store.Save(d.VaultPath, art.ID, art.Filename, data); err != nil {
			return nil, fmt.Errorf("save artifact: %w", err)
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
		return nil, fmt.Errorf("update runs: %w", err)
	}

	// Re-pend the job and cascade to downstream stages.
	if err := d.Jobs.UpdateStatus(ctx, job.ID, string(model.JobStatusPending), now); err != nil {
		return nil, fmt.Errorf("update status: %w", err)
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
		return nil, fmt.Errorf("cascade replay: %w", err)
	}
	downstream := downstreamStages(stageOrder, stageName)
	slog.Info("stage artifact updated via chat tool",
		"doc_id", ShortID(docID), "stage", stageName, "field", field,
		"bytes", len(data), "downstream", downstream)
	return downstream, nil
}

// StageOutputFields returns every output field a stage produces. Stages declare
// outputs either as a single `output:` (summarize, clarify) or a list
// `outputs:` (transcribe, ocr, classify) in pipeline.yaml; both forms must be
// considered or fields like clarified_text/narrative_summary appear unknown.
func StageOutputFields(s model.StageDefinition) []string {
	fields := make([]string, 0, len(s.Outputs)+1)
	if s.Output != "" {
		fields = append(fields, s.Output)
	}
	for _, o := range s.Outputs {
		fields = append(fields, o.Field)
	}
	return fields
}

// resolveFieldStage looks up which stage in the configured pipeline produces
// the named output field. Returns ErrUnknownField if no stage matches.
func resolveFieldStage(p model.PipelineConfig, field string) (string, error) {
	for _, s := range p.Stages {
		for _, f := range StageOutputFields(s) {
			if f == field {
				return s.Name, nil
			}
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownField, field)
}

// canonicalBodyFields are the document-body output fields, most-polished first.
// update_document edits whichever the document actually has so a doc whose
// clarify stage was skipped still has an editable body.
var canonicalBodyFields = []string{
	model.FieldClarifiedText,
	model.FieldNarrativeSummary,
	model.FieldRawText,
}

// ResolveCanonicalBody returns the field + stage of a document's canonical
// body: clarified_text when clarify has completed, otherwise the most-polished
// body stage that did (narrative_summary, then raw_text). The pipeline is
// walked in reverse so the latest body stage wins, and both transcribe and ocr
// (which share raw_text) are matched by inspecting the actual stages. Returns
// ErrNoCanonicalBody when no body stage has produced output yet.
func ResolveCanonicalBody(ctx context.Context, d StageUpdateDeps, docID string) (field, stage string, err error) {
	for i := len(d.Pipeline.Stages) - 1; i >= 0; i-- {
		s := d.Pipeline.Stages[i]
		bodyField := bodyOutputOf(s)
		if bodyField == "" {
			continue
		}
		_, _, _, ferr := findDoneStageRun(ctx, d, docID, s.Name, bodyField)
		switch {
		case ferr == nil:
			return bodyField, s.Name, nil
		case errors.Is(ferr, ErrNoJob), errors.Is(ferr, ErrNoArtifact):
			// Stage was skipped or produced no output for this doc — fall back
			// to a less-polished body field (e.g. clarify skipped → narrative_summary).
			continue
		default:
			// ErrStageNotDone (the body stage is still running) or an
			// infrastructure error (e.g. listing jobs failed). Don't silently
			// edit an upstream draft or report "no body" — surface it so the
			// caller can tell the user to wait or retry.
			return "", "", ferr
		}
	}
	return "", "", fmt.Errorf("%w for document %s", ErrNoCanonicalBody, ShortID(docID))
}

// bodyOutputOf returns the body field a stage produces (clarified_text,
// narrative_summary, or raw_text), or "" if it produces none.
func bodyOutputOf(s model.StageDefinition) string {
	for _, f := range StageOutputFields(s) {
		for _, body := range canonicalBodyFields {
			if f == body {
				return f
			}
		}
	}
	return ""
}

// CurrentCanonicalBody returns the current text of the document's canonical
// body along with the stage and field it resolved to (for the approval card).
// It reads at the resolved stage rather than re-deriving it from the field, so
// a raw_text body produced by ocr is not mistakenly looked up under transcribe.
func CurrentCanonicalBody(ctx context.Context, d StageUpdateDeps, docID string) (current, stage, field string, err error) {
	field, stage, err = ResolveCanonicalBody(ctx, d, docID)
	if err != nil {
		return "", "", "", err
	}
	current, err = currentStageOutputAt(ctx, d, docID, stage, field)
	return current, stage, field, err
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
