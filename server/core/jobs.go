package core

import "github.com/fagerbergj/document-pipeline/server/core/model"

// PickCurrentJob returns the most relevant job for display.
// Priority: running > waiting > pending > error > done (most recently updated).
func PickCurrentJob(jobs []model.Job) *model.Job {
	if len(jobs) == 0 {
		return nil
	}
	for _, status := range []model.JobStatus{
		model.JobStatusRunning,
		model.JobStatusWaiting,
		model.JobStatusPending,
		model.JobStatusError,
	} {
		for i := range jobs {
			if jobs[i].Status == status {
				return &jobs[i]
			}
		}
	}
	// All done — return most recently updated
	latest := &jobs[0]
	for i := range jobs[1:] {
		if jobs[i+1].UpdatedAt.After(latest.UpdatedAt) {
			latest = &jobs[i+1]
		}
	}
	return latest
}
