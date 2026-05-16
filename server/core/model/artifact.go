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
	Path *string
	// Stage and Field identify which pipeline stage and output field this
	// artifact represents. Set on user-seeded artifacts at upload time so the
	// worker can detect and consume them in place of running the stage. Null on
	// artifacts created automatically by stage execution.
	Stage     *string
	Field     *string
	CreatedAt time.Time
	UpdatedAt time.Time
}
