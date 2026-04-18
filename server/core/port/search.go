package port

import "context"

// IndexDoc is the document representation stored in OpenSearch.
type IndexDoc struct {
	DocID     string
	Title     string
	Series    string
	Content   string   // clarified_text from clarify stage
	Summary   string   // from classify stage
	Tags      []string // from classify stage
	DateMonth string
	Stage     string // current job stage
	Status    string // current job status
}

// DocumentIndexer manages the OpenSearch document index.
type DocumentIndexer interface {
	EnsureIndex(ctx context.Context) error
	Count(ctx context.Context) (int, error)
	Index(ctx context.Context, doc IndexDoc) error
	Delete(ctx context.Context, docID string) error
	Search(ctx context.Context, query string, size int) ([]string, error)
}
