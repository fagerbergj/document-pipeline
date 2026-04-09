package model

import "time"

type Artifact struct {
	ID           string
	DocumentID   string
	Filename     string
	ContentType  string
	CreatedJobID *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
