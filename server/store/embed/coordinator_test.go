package embed_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/fagerbergj/document-pipeline/server/store/embed"
)

// --- fakes ---

type fakeQdrant struct {
	upserted []string
	deleted  []string
	err      error
}

func (f *fakeQdrant) Upsert(_ context.Context, id string, _ []float32, _ []float32, _ map[string]any) error {
	f.upserted = append(f.upserted, id)
	return f.err
}
func (f *fakeQdrant) Search(_ context.Context, _ []float32, _ int) ([]port.EmbedResult, error) {
	return []port.EmbedResult{{ID: "r1", Score: 0.9}}, f.err
}
func (f *fakeQdrant) DeleteByDocID(_ context.Context, docID string) error {
	f.deleted = append(f.deleted, docID)
	return f.err
}
func (f *fakeQdrant) GetByIDs(_ context.Context, _ []string) ([]port.EmbedResult, error) {
	return nil, nil
}
func (f *fakeQdrant) DeleteBySeries(_ context.Context, series string) error {
	f.deleted = append(f.deleted, series)
	return f.err
}

type fakeWebUI struct {
	upserted []string
	deleted  []string
	err      error
}

func (f *fakeWebUI) Upsert(_ context.Context, docID, _, _ string, _ map[string]any) error {
	f.upserted = append(f.upserted, docID)
	return f.err
}
func (f *fakeWebUI) Delete(_ context.Context, docID string) error {
	f.deleted = append(f.deleted, docID)
	return f.err
}

// --- tests ---

func TestCoordinator_Upsert(t *testing.T) {
	q := &fakeQdrant{}
	w := &fakeWebUI{}
	c := embed.New(q, w)

	if err := c.Upsert(context.Background(), "doc-1", []float32{0.1}, nil, map[string]any{port.PayloadTitle: "T", port.PayloadText: "hello"}); err != nil {
		t.Fatal(err)
	}
	if len(q.upserted) != 1 || q.upserted[0] != "doc-1" {
		t.Error("expected qdrant upsert")
	}
	if len(w.upserted) != 1 || w.upserted[0] != "doc-1" {
		t.Error("expected webui upsert")
	}
}

func TestCoordinator_WebUIError_NonFatal(t *testing.T) {
	q := &fakeQdrant{}
	w := &fakeWebUI{err: errors.New("webui down")}
	c := embed.New(q, w)

	// Should succeed even when webui fails.
	if err := c.Upsert(context.Background(), "doc-1", []float32{0.1}, nil, map[string]any{}); err != nil {
		t.Fatalf("expected non-fatal webui error, got: %v", err)
	}
}

func TestCoordinator_QdrantError_Fatal(t *testing.T) {
	q := &fakeQdrant{err: errors.New("qdrant down")}
	w := &fakeWebUI{}
	c := embed.New(q, w)

	if err := c.Upsert(context.Background(), "doc-1", []float32{0.1}, nil, map[string]any{}); err == nil {
		t.Fatal("expected qdrant error to propagate")
	}
	if len(w.upserted) != 0 {
		t.Error("webui should not be called when qdrant fails")
	}
}

func TestCoordinator_Search(t *testing.T) {
	q := &fakeQdrant{}
	c := embed.NewQdrantOnly(q)

	results, err := c.Search(context.Background(), []float32{0.1}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestCoordinator_Delete(t *testing.T) {
	q := &fakeQdrant{}
	w := &fakeWebUI{}
	c := embed.New(q, w)

	if err := c.DeleteByDocID(context.Background(), "doc-1"); err != nil {
		t.Fatal(err)
	}
	if len(q.deleted) != 1 {
		t.Error("expected qdrant delete")
	}
	if len(w.deleted) != 1 {
		t.Error("expected webui delete")
	}
}

func TestCoordinator_QdrantOnly_NoWebUI(t *testing.T) {
	q := &fakeQdrant{}
	c := embed.NewQdrantOnly(q)

	if err := c.Upsert(context.Background(), "doc-1", []float32{0.1}, nil, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteByDocID(context.Background(), "doc-1"); err != nil {
		t.Fatal(err)
	}
}
