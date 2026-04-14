// Package embed provides EmbedStoreCoordinator, which implements port.EmbedStore
// by coordinating a Qdrant vector store and an Open WebUI knowledge base.
package embed

import (
	"context"
	"log/slog"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// qdrantStore is the subset of qdrant.Client used here.
type qdrantStore interface {
	Upsert(ctx context.Context, id string, textVector []float32, imageVector []float32, payload map[string]any) error
	Search(ctx context.Context, vector []float32, topK int) ([]port.EmbedResult, error)
	Delete(ctx context.Context, id string) error
}

// webUIStore is the subset of openwebui.Client used here.
type webUIStore interface {
	Upsert(ctx context.Context, docID, title, text string, metadata map[string]any) error
	Delete(ctx context.Context, docID string) error
}

// EmbedStoreCoordinator implements port.EmbedStore.
// It writes embeddings to Qdrant and syncs document text to Open WebUI.
// Open WebUI errors are logged but do not fail the operation.
type EmbedStoreCoordinator struct {
	qdrant   qdrantStore
	webUI    webUIStore
	useWebUI bool
}

var _ port.EmbedStore = (*EmbedStoreCoordinator)(nil)

// New returns a coordinator backed by both Qdrant and Open WebUI.
func New(qdrant qdrantStore, webUI webUIStore) *EmbedStoreCoordinator {
	return &EmbedStoreCoordinator{qdrant: qdrant, webUI: webUI, useWebUI: true}
}

// NewQdrantOnly returns a coordinator backed only by Qdrant (no Open WebUI).
func NewQdrantOnly(qdrant qdrantStore) *EmbedStoreCoordinator {
	return &EmbedStoreCoordinator{qdrant: qdrant}
}

// Upsert stores the embedding in Qdrant and syncs the document to Open WebUI.
// The payload may contain "title" and "text" keys used by Open WebUI.
func (c *EmbedStoreCoordinator) Upsert(ctx context.Context, id string, textVector []float32, imageVector []float32, payload map[string]any) error {
	if err := c.qdrant.Upsert(ctx, id, textVector, imageVector, payload); err != nil {
		return err
	}
	if c.useWebUI {
		title, _ := payload["title"].(string)
		text, _ := payload["text"].(string)
		meta := make(map[string]any, len(payload))
		for k, v := range payload {
			if k == "title" || k == "text" {
				continue
			}
			meta[k] = v
		}
		if err := c.webUI.Upsert(ctx, id, title, text, meta); err != nil {
			slog.Warn("open webui upsert failed (non-fatal)", "err", err, "doc_id", shortID(id))
		}
	}
	return nil
}

// Search queries Qdrant for the nearest neighbours of vector.
func (c *EmbedStoreCoordinator) Search(ctx context.Context, vector []float32, topK int) ([]port.EmbedResult, error) {
	return c.qdrant.Search(ctx, vector, topK)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// Delete removes the embedding from Qdrant and the document from Open WebUI.
func (c *EmbedStoreCoordinator) Delete(ctx context.Context, id string) error {
	if err := c.qdrant.Delete(ctx, id); err != nil {
		return err
	}
	if c.useWebUI {
		if err := c.webUI.Delete(ctx, id); err != nil {
			slog.Warn("open webui delete failed (non-fatal)", "err", err, "doc_id", shortID(id))
		}
	}
	return nil
}
