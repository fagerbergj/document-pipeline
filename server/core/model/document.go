package model

import "time"

type Document struct {
	ID                string
	ContentHash       string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	Title             *string
	DateMonth         *string
	PNGPath           *string
	DuplicateOf       *string
	AdditionalContext string
	LinkedContexts    []string // context entry IDs
}
