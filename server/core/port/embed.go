package port

import "context"

// EmbedStore stores and retrieves document embeddings.
// Implemented by store/embed.EmbedStoreCoordinator, which coordinates
// a vector store (Qdrant) and a search index (Open WebUI) internally.
type EmbedStore interface {
	Upsert(ctx context.Context, id string, textVector []float32, imageVector []float32, payload map[string]any) error
	Search(ctx context.Context, vector []float32, topK int) ([]EmbedResult, error)
	Delete(ctx context.Context, id string) error
}

type EmbedResult struct {
	ID      string
	Score   float64
	Payload map[string]any
}
