package model

import "time"

type FileType string

const (
	FileTypePNG  FileType = "png"
	FileTypeJPG  FileType = "jpg"
	FileTypeJPEG FileType = "jpeg"
	FileTypeTXT  FileType = "txt"
	FileTypeMD   FileType = "md"
	FileTypeWEBM FileType = "webm"
	FileTypeWAV  FileType = "wav"
	FileTypeMP3  FileType = "mp3"
	FileTypeM4A  FileType = "m4a"
	FileTypeOGG  FileType = "ogg"
	FileTypeFLAC FileType = "flac"
)

// IsImage reports whether the file type is a still image.
func (f FileType) IsImage() bool {
	return f == FileTypePNG || f == FileTypeJPG || f == FileTypeJPEG
}

// IsAudio reports whether the file type is recorded audio.
func (f FileType) IsAudio() bool {
	switch f {
	case FileTypeWEBM, FileTypeWAV, FileTypeMP3, FileTypeM4A, FileTypeOGG, FileTypeFLAC:
		return true
	}
	return false
}

// IsText reports whether the file type is plain text or markdown.
func (f FileType) IsText() bool { return f == FileTypeTXT || f == FileTypeMD }

type Document struct {
	ID          string
	ContentHash string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Title       *string
	DateMonth   *string
	// MediaPath is the vault path to the source media file (image, audio, etc.).
	// Stages branch on the document's file type to decide how to consume it.
	MediaPath         *string
	DuplicateOf       *string
	AdditionalContext string
	LinkedContexts    []string // context entry IDs
	Series            *string
}
