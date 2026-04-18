package embed

import (
	"context"
	"log/slog"
	"sync"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// NoopStore implements port.EmbedStore without any backend. Used when Qdrant is
// unconfigured so the service still boots; Upsert/Delete silently succeed and
// Search returns no sources, but the first Search call logs a warning so chat
// queries that degrade to "no sources" leave a breadcrumb instead of failing
// silently.
type NoopStore struct {
	warnOnce sync.Once
}

// NewNoop returns an EmbedStore that drops writes and returns empty results.
func NewNoop() *NoopStore {
	return &NoopStore{}
}

func (s *NoopStore) Upsert(_ context.Context, _ string, _ []float32, _ []float32, _ map[string]any) error {
	return nil
}

func (s *NoopStore) Search(_ context.Context, _ []float32, _ int) ([]port.EmbedResult, error) {
	s.warnOnce.Do(func() {
		slog.Warn("embed store disabled — Search returning no sources (set QDRANT_URL to enable)")
	})
	return nil, nil
}

func (s *NoopStore) DeleteByDocID(_ context.Context, _ string) error  { return nil }
func (s *NoopStore) DeleteBySeries(_ context.Context, _ string) error { return nil }
