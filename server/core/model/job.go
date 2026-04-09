package model

import "time"

type Job struct {
	ID         string
	DocumentID string
	Stage      string
	Status     string // pending|running|waiting|error|done
	Options    JobOptions
	Runs       []Run
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type JobOptions struct {
	RequireContext bool      `json:"require_context,omitempty"`
	Embed         *EmbedOpts `json:"embed,omitempty"`
}

type EmbedOpts struct {
	EmbedImage bool `json:"embed_image,omitempty"`
}

type Run struct {
	ID          string
	Inputs      []Field
	Outputs     []Field
	Confidence  string
	Questions   []Question
	Suggestions Suggestions
	CreatedAt   time.Time
	UpdatedAt   time.Time
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

type Suggestions struct {
	AdditionalContext string  `json:"additional_context"`
	LinkedContext     string  `json:"linked_context"`
	LinkedContextID   *string `json:"linked_context_id"`
}

const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusWaiting = "waiting"
	StatusError   = "error"
	StatusDone    = "done"
)
