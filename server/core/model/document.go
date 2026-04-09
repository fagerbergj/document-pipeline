package model

import "time"

type FileType string

const (
	FileTypePNG  FileType = "png"
	FileTypeJPG  FileType = "jpg"
	FileTypeJPEG FileType = "jpeg"
	FileTypeTXT  FileType = "txt"
	FileTypeMD   FileType = "md"
)

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
