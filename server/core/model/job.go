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

type Field struct {
	Field string `json:"field"`
	Text  string `json:"text"`
}

type Question struct {
	Segment  string `json:"segment"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
}
