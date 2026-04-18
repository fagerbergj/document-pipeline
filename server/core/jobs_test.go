package core

import (
	"testing"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/model"
)

func TestPickCurrentJob_Priority(t *testing.T) {
	now := time.Now()
	jobs := []model.Job{
		{ID: "a", Status: model.JobStatusDone, UpdatedAt: now.Add(-1 * time.Hour)},
		{ID: "b", Status: model.JobStatusPending, UpdatedAt: now},
		{ID: "c", Status: model.JobStatusWaiting, UpdatedAt: now},
	}
	got := PickCurrentJob(jobs)
	if got == nil || got.ID != "c" {
		t.Errorf("expected waiting job 'c', got %v", got)
	}
}

func TestPickCurrentJob_RunningBeatsWaiting(t *testing.T) {
	now := time.Now()
	jobs := []model.Job{
		{ID: "a", Status: model.JobStatusWaiting, UpdatedAt: now},
		{ID: "b", Status: model.JobStatusRunning, UpdatedAt: now.Add(-time.Minute)},
	}
	got := PickCurrentJob(jobs)
	if got == nil || got.ID != "b" {
		t.Errorf("expected running job 'b', got %v", got)
	}
}

func TestPickCurrentJob_AllDone(t *testing.T) {
	now := time.Now()
	jobs := []model.Job{
		{ID: "a", Status: model.JobStatusDone, UpdatedAt: now.Add(-1 * time.Minute)},
		{ID: "b", Status: model.JobStatusDone, UpdatedAt: now},
	}
	got := PickCurrentJob(jobs)
	if got == nil || got.ID != "b" {
		t.Errorf("expected most recent 'b', got %v", got)
	}
}

func TestPickCurrentJob_Empty(t *testing.T) {
	if got := PickCurrentJob(nil); got != nil {
		t.Errorf("expected nil for empty slice, got %v", got)
	}
}
