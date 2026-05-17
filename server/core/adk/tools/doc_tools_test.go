package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// realIndexer is a minimal port.DocumentIndexer impl used by these tests.
type realIndexer struct {
	ids   []string
	total int
}

func (r realIndexer) EnsureIndex(_ context.Context) error            { return nil }
func (r realIndexer) Count(_ context.Context) (int, error)           { return 0, nil }
func (r realIndexer) Index(_ context.Context, _ port.IndexDoc) error { return nil }
func (r realIndexer) Delete(_ context.Context, _ string) error       { return nil }
func (r realIndexer) Search(_ context.Context, _ string, _, _ int) ([]string, int, error) {
	return r.ids, r.total, nil
}

// strPtr is a tiny helper for setting pointer fields in test fixtures.
func strPtr(s string) *string { return &s }

func TestSearchDocuments_HappyPath(t *testing.T) {
	getDoc := func(_ context.Context, id string) (model.Document, error) {
		return model.Document{ID: id, Title: strPtr("Title " + id), DateMonth: strPtr("2026-05")}, nil
	}
	stageData := func(_ context.Context, id string) (map[string]map[string]any, error) {
		return map[string]map[string]any{
			"classify": {
				"summary": "summary for " + id,
				"tags":    []any{"foo", "bar"},
			},
		}, nil
	}

	res, err := runSearchDocuments(context.Background(),
		realIndexer{ids: []string{"doc-1", "doc-2"}, total: 2},
		getDoc, stageData, 10,
		SearchDocumentsArgs{Query: "hello"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("want 2 results, got %d", len(res.Results))
	}
	r0 := res.Results[0]
	if r0.ID != "doc-1" || r0.Title != "Title doc-1" || r0.Summary != "summary for doc-1" {
		t.Errorf("hit 0 wrong: %+v", r0)
	}
	if got := r0.Tags; len(got) != 2 || got[0] != "foo" || got[1] != "bar" {
		t.Errorf("hit 0 tags: %+v", got)
	}
}

func TestSearchDocuments_SkipMissingDocs(t *testing.T) {
	getDoc := func(_ context.Context, id string) (model.Document, error) {
		if id == "doc-missing" {
			return model.Document{}, errors.New("not found")
		}
		return model.Document{ID: id}, nil
	}
	stageData := func(_ context.Context, _ string) (map[string]map[string]any, error) {
		return nil, nil
	}
	res, err := runSearchDocuments(context.Background(),
		realIndexer{ids: []string{"doc-ok", "doc-missing"}, total: 2},
		getDoc, stageData, 10,
		SearchDocumentsArgs{Query: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 1 || res.Results[0].ID != "doc-ok" {
		t.Errorf("expected only doc-ok returned, got %+v", res.Results)
	}
}

func TestGetDocument_HappyPath(t *testing.T) {
	getDoc := func(_ context.Context, id string) (model.Document, error) {
		return model.Document{ID: id, Title: strPtr("Hello"), DateMonth: strPtr("2026-05")}, nil
	}
	stageData := func(_ context.Context, _ string) (map[string]map[string]any, error) {
		return map[string]map[string]any{
			"clarify": {"clarified_text": "the full polished text"},
			"classify": {
				"summary": "short abstract",
				"tags":    []string{"a", "b"},
			},
		}, nil
	}
	res, err := runGetDocument(context.Background(), getDoc, stageData, GetDocumentArgs{ID: "doc-1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.FullText != "the full polished text" {
		t.Errorf("FullText: %q", res.FullText)
	}
	if res.Summary != "short abstract" || res.Title != "Hello" || res.DateMonth != "2026-05" {
		t.Errorf("metadata wrong: %+v", res)
	}
	if len(res.Tags) != 2 || res.Tags[0] != "a" {
		t.Errorf("tags wrong: %+v", res.Tags)
	}
}

func TestGetDocument_DocNotFound(t *testing.T) {
	getDoc := func(_ context.Context, _ string) (model.Document, error) {
		return model.Document{}, errors.New("not found")
	}
	stageData := func(_ context.Context, _ string) (map[string]map[string]any, error) {
		return nil, nil
	}
	_, err := runGetDocument(context.Background(), getDoc, stageData, GetDocumentArgs{ID: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
}

