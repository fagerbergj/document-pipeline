// Package embed provides EmbedStoreCoordinator, which implements port.EmbedStore
// by delegating to a Qdrant vector store.
package embed

import (
	"context"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// qdrantStore is the subset of qdrant.Client used here.
type qdrantStore interface {
	Upsert(ctx context.Context, id string, textVector []float32, imageVector []float32, payload map[string]any) error
	Search(ctx context.Context, queryText string, vector []float32, topK int) ([]port.EmbedResult, error)
	GetByIDs(ctx context.Context, ids []string) ([]port.EmbedResult, error)
	DeleteByDocID(ctx context.Context, docID string) error
	DeleteBySeries(ctx context.Context, series string) error
	EnsurePayloadIndexes(ctx context.Context, fields []string) error
}

// EmbedStoreCoordinator implements port.EmbedStore over a Qdrant backend.
type EmbedStoreCoordinator struct {
	qdrant qdrantStore
}

var _ port.EmbedStore = (*EmbedStoreCoordinator)(nil)

// New returns a coordinator backed by Qdrant.
func New(qdrant qdrantStore) *EmbedStoreCoordinator {
	return &EmbedStoreCoordinator{qdrant: qdrant}
}

// Qdrant returns the underlying Qdrant client for advanced operations.
func (c *EmbedStoreCoordinator) Qdrant() qdrantStore {
	return c.qdrant
}

// Upsert stores the embedding in Qdrant.
func (c *EmbedStoreCoordinator) Upsert(ctx context.Context, id string, textVector []float32, imageVector []float32, payload map[string]any) error {
	return c.qdrant.Upsert(ctx, id, textVector, imageVector, payload)
}

// Search queries Qdrant for hybrid (dense + sparse BM25) matches when the
// collection supports sparse vectors, falling back to dense-only otherwise.
func (c *EmbedStoreCoordinator) Search(ctx context.Context, queryText string, vector []float32, topK int) ([]port.EmbedResult, error) {
	return c.qdrant.Search(ctx, queryText, vector, topK)
}

// GetByIDs fetches specific points from Qdrant by their chunk string IDs.
func (c *EmbedStoreCoordinator) GetByIDs(ctx context.Context, ids []string) ([]port.EmbedResult, error) {
	return c.qdrant.GetByIDs(ctx, ids)
}

// DeleteByDocID removes all chunk embeddings for a document from Qdrant.
func (c *EmbedStoreCoordinator) DeleteByDocID(ctx context.Context, docID string) error {
	return c.qdrant.DeleteByDocID(ctx, docID)
}

// DeleteBySeries removes all series corpus embeddings from Qdrant.
func (c *EmbedStoreCoordinator) DeleteBySeries(ctx context.Context, series string) error {
	return c.qdrant.DeleteBySeries(ctx, series)
}

// EnsurePayloadIndexes creates payload indexes for the specified fields.
func (c *EmbedStoreCoordinator) EnsurePayloadIndexes(ctx context.Context, fields []string) error {
	return c.qdrant.EnsurePayloadIndexes(ctx, fields)
}
