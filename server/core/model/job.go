package model

import "time"

type JobStatus string

const (
	JobStatusPending JobStatus = "pending"
	JobStatusRunning JobStatus = "running"
	JobStatusWaiting JobStatus = "waiting"
	JobStatusError   JobStatus = "error"
	JobStatusDone    JobStatus = "done"
)

type Job struct {
	ID         string
	DocumentID string
	Stage      string
	Status     JobStatus
	Options    JobOptions
	Runs       []Run
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type JobOptions struct {
	RequireContext bool       `json:"require_context,omitempty"`
	Embed          *EmbedOpts `json:"embed,omitempty"`
}

type EmbedOpts struct {
	EmbedImage bool `json:"embed_image,omitempty"`
}

type Confidence string

const (
	ConfidenceLow    Confidence = "low"
	ConfidenceMedium Confidence = "medium"
	ConfidenceHigh   Confidence = "high"
)

type Run struct {
	ID         string
	Inputs     []Field
	Outputs    []Field
	Confidence Confidence
	Questions  []Question
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Field is a stage input or output. Text content lives on disk and is exposed
// as a row in the artifacts table; only the artifact id, size, and a short
// preview are persisted in the jobs.runs JSON column. The frontend fetches the
// full value via GET /api/v1/documents/{doc_id}/artifacts/{artifact_id}.
type Field struct {
	Field      string `json:"field"`
	ArtifactID string `json:"artifact_id,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Preview    string `json:"preview,omitempty"`
}

type Question struct {
	Segment  string `json:"segment"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
}
