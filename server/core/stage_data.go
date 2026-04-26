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
	stageData := map[string]map[string]any{}
	for _, j := range jobList {
		if (j.Status == model.JobStatusDone || j.Status == model.JobStatusWaiting) && len(j.Runs) > 0 {
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
	}
	return stageData, nil
}
