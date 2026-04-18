package port

import "context"

// Payload keys written by WorkerService and read by EmbedStore consumers.
const (
	PayloadTitle      = "title"
	PayloadText       = "text"
	PayloadDocID      = "doc_id"
	PayloadDateMonth  = "date_month"
	PayloadSummary    = "summary"
	PayloadChunkIndex = "chunk_index"
	PayloadSeriesName = "series_name"
)

// EmbedStore stores and retrieves document embeddings.
// Implemented by store/embed.EmbedStoreCoordinator, which coordinates
// a vector store (Qdrant) and a search index (Open WebUI) internally.
type EmbedStore interface {
	Upsert(ctx context.Context, id string, textVector []float32, imageVector []float32, payload map[string]any) error
	Search(ctx context.Context, vector []float32, topK int) ([]EmbedResult, error)
	DeleteByDocID(ctx context.Context, docID string) error
	DeleteBySeries(ctx context.Context, series string) error
}

type EmbedResult struct {
	ID      string
	Score   float64
	Payload map[string]any
}
