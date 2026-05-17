package core

import (
	"context"
	"log/slog"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// CollectStageData returns the latest outputs from each completed or waiting job
// for the given document, keyed by stage name. Output text is loaded from the
// artifact backing each Field.
func CollectStageData(ctx context.Context, jobs port.JobRepo, artifacts port.ArtifactRepo, store port.DocumentArtifactStore, vaultPath, docID string) (map[string]map[string]any, error) {
	jobList, err := jobs.ListForDocument(ctx, docID)
	if err != nil {
		return nil, err
	}
	return stageDataFromJobs(ctx, jobList, artifacts, store, vaultPath, docID), nil
}

// CollectStageDataBatch returns CollectStageData results for many documents
// in a single jobs query. Useful for tools that resolve a search-results list
// of doc ids — it collapses N "list jobs for doc" round trips into one.
func CollectStageDataBatch(ctx context.Context, jobs port.JobRepo, artifacts port.ArtifactRepo, store port.DocumentArtifactStore, vaultPath string, docIDs []string) (map[string]map[string]map[string]any, error) {
	out := make(map[string]map[string]map[string]any, len(docIDs))
	if len(docIDs) == 0 {
		return out, nil
	}
	// One query for all jobs across the doc set.
	page := model.PageRequest{PageSize: len(docIDs) * 16}
	result, err := jobs.ListPaginated(ctx, port.JobFilter{DocumentIDs: docIDs}, page)
	if err != nil {
		return nil, err
	}
	byDoc := make(map[string][]model.Job, len(docIDs))
	for _, j := range result.Data {
		byDoc[j.DocumentID] = append(byDoc[j.DocumentID], j)
	}
	for _, id := range docIDs {
		out[id] = stageDataFromJobs(ctx, byDoc[id], artifacts, store, vaultPath, id)
	}
	return out, nil
}

func stageDataFromJobs(ctx context.Context, jobList []model.Job, artifacts port.ArtifactRepo, store port.DocumentArtifactStore, vaultPath, docID string) map[string]map[string]any {
	stageData := map[string]map[string]any{}
	for _, j := range jobList {
		if (j.Status != model.JobStatusDone && j.Status != model.JobStatusWaiting) || len(j.Runs) == 0 {
			continue
		}
		latest := j.Runs[len(j.Runs)-1]
		outputs := map[string]any{}
		for _, f := range latest.Outputs {
			if f.Field == "" || f.ArtifactID == "" {
				continue
			}
			art, err := artifacts.Get(ctx, docID, f.ArtifactID)
			if err != nil {
				slog.Warn("could not load output artifact", "job_id", j.ID[:8], "field", f.Field, "artifact_id", f.ArtifactID, "err", err)
				continue
			}
			text, err := readArtifactText(store, vaultPath, art)
			if err != nil {
				slog.Warn("could not read output artifact file", "job_id", j.ID[:8], "field", f.Field, "err", err)
				continue
			}
			outputs[f.Field] = text
		}
		if len(outputs) > 0 {
			stageData[j.Stage] = outputs
		}
	}
	return stageData
}
