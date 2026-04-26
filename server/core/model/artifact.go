package model

import "time"

type Artifact struct {
	ID           string
	DocumentID   string
	Filename     string
	ContentType  string
	CreatedJobID *string
	// Path is the vault-relative location of the file. When empty, the legacy
	// layout `<vault>/artifacts/<id>/<filename>` is used. Run-output artifacts
	// (auto-created by the worker for each stage output) populate Path so they
	// can live under organized `<vault>/runs/<job>/<run>/<field>.<ext>` paths
	// while still being addressable via the artifacts table.
	Path      *string
	CreatedAt time.Time
	UpdatedAt time.Time
}
