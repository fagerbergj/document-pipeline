package model

import "time"

type StageEvent struct {
	ID         int64
	DocumentID string
	Timestamp  time.Time
	Stage      string
	EventType  string
	Data       map[string]any
}

const (
	EventReceived  = "received"
	EventStarted   = "started"
	EventCompleted = "completed"
	EventFailed    = "failed"
)
