package qdrant_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fagerbergj/document-pipeline/server/store/qdrant"
)

// namedVectorsHandler simulates a Qdrant collection that uses named vectors.
func namedCollectionInfo() map[string]any {
	return map[string]any{
		"result": map[string]any{
			"config": map[string]any{
				"params": map[string]any{
					"vectors": map[string]any{
						"text":  map[string]any{"size": 4, "distance": "Cosine"},
						"image": map[string]any{"size": 4, "distance": "Cosine"},
					},
				},
			},
		},
	}
}

func TestUpsert_CreatesCollection(t *testing.T) {
	created := false
	upserted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections/docs":
			if !created {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(namedCollectionInfo())
		case r.Method == http.MethodPut && r.URL.Path == "/collections/docs":
			created = true
			json.NewEncoder(w).Encode(map[string]any{"result": true})
		case r.Method == http.MethodPut && r.URL.Path == "/collections/docs/points":
			upserted = true
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": "ok"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := qdrant.New(srv.URL, "docs", "")
	err := c.Upsert(context.Background(), "550e8400-e29b-41d4-a716-446655440000",
		[]float32{0.1, 0.2, 0.3, 0.4},
		[]float32{0.5, 0.6, 0.7, 0.8},
		map[string]any{"text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("expected collection to be created")
	}
	if !upserted {
		t.Error("expected point to be upserted")
	}
}

func TestUpsert_ExistingCollection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections/docs":
			json.NewEncoder(w).Encode(namedCollectionInfo())
		case r.Method == http.MethodPut && r.URL.Path == "/collections/docs/points":
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": "ok"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := qdrant.New(srv.URL, "docs", "")
	err := c.Upsert(context.Background(), "550e8400-e29b-41d4-a716-446655440000",
		[]float32{0.1, 0.2, 0.3, 0.4}, nil, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDelete(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/collections/docs/points/delete" {
			deleted = true
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": "acknowledged"}})
		} else {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := qdrant.New(srv.URL, "docs", "")
	if err := c.Delete(context.Background(), "550e8400-e29b-41d4-a716-446655440000"); err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Error("expected delete to be called")
	}
}

func TestSearch_CollectionMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := qdrant.New(srv.URL, "docs", "")
	results, err := c.Search(context.Background(), []float32{0.1, 0.2}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestSearch_ReturnsResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections/docs":
			// unnamed vectors
			json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"config": map[string]any{
						"params": map[string]any{
							"vectors": map[string]any{"size": 2, "distance": "Cosine"},
						},
					},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/collections/docs/points/search":
			json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"id": 1, "score": 0.95, "payload": map[string]any{"doc_id": "abc"}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := qdrant.New(srv.URL, "docs", "")
	results, err := c.Search(context.Background(), []float32{0.1, 0.2}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Score != 0.95 {
		t.Errorf("expected score 0.95, got %f", results[0].Score)
	}
}
