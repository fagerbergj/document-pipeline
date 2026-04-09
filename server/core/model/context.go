package model

import "time"

// Context is a reusable context snippet that can be linked to documents.
type Context struct {
	ID        string
	Name      string
	Text      string
	CreatedAt time.Time
}
