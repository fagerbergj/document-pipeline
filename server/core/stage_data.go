package core

import (
	"context"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// CollectStageData returns the latest outputs from each completed or waiting job
// for the given document, keyed by stage name.
func CollectStageData(ctx context.Context, jobs port.JobRepo, docID string) (map[string]map[string]any, error) {
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
