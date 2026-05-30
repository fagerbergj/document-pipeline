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
	PayloadPrevChunk  = "prev_chunk_id"
	PayloadNextChunk  = "next_chunk_id"
	// PayloadContext is the LLM-produced situating context generated when an
	// embed stage runs with contextual_model set. Stored so retrieval results
	// can show both the chunk and the situating sentence.
	PayloadContext = "context"
)

// EmbedStore stores and retrieves document embeddings.
// Implemented by store/embed.EmbedStoreCoordinator over Qdrant.
//
// Search performs hybrid retrieval (dense vector + BM25 sparse) when the
// underlying store supports it. queryText is the raw user query used to build
// the sparse half; vector is the dense embedding of the same query. Pass an
// empty queryText to force dense-only search.
type EmbedStore interface {
	Upsert(ctx context.Context, id string, textVector []float32, imageVector []float32, payload map[string]any) error
	Search(ctx context.Context, queryText string, vector []float32, topK int) ([]EmbedResult, error)
	GetByIDs(ctx context.Context, ids []string) ([]EmbedResult, error)
	DeleteByDocID(ctx context.Context, docID string) error
	DeleteBySeries(ctx context.Context, series string) error
	// EnsurePayloadIndexes creates payload indexes for the specified fields.
	// This enables fast filtering on metadata fields like title, tags, summary.
	EnsurePayloadIndexes(ctx context.Context, fields []string) error
}

type EmbedResult struct {
	ID      string
	Score   float64
	Payload map[string]any
}
