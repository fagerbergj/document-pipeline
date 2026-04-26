package port

import "context"

// Transcriber sends audio bytes to a speech-to-text service and returns the
// transcribed text. The filename is forwarded as the multipart upload's
// filename so the service can sniff the audio container format from the
// extension when no Content-Type is provided.
type Transcriber interface {
	Transcribe(ctx context.Context, model string, audio []byte, filename string) (string, error)
}
