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

// TestUpsert_RecreatesCollectionOn404 simulates the collection being dropped out
// of band (e.g. by a re-embed) after the client has cached exists=true: the
// first upsert 404s, and the client must invalidate its cache, recreate the
// collection, and retry the upsert rather than surfacing the 404.
func TestUpsert_RecreatesCollectionOn404(t *testing.T) {
	exists := true // starts existing → first ensureCollection caches exists=true
	var createCalls, pointsCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections/docs":
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(namedCollectionInfo())
		case r.Method == http.MethodPut && r.URL.Path == "/collections/docs":
			createCalls++
			exists = true
			json.NewEncoder(w).Encode(map[string]any{"result": true})
		case r.Method == http.MethodPut && r.URL.Path == "/collections/docs/points":
			pointsCalls++
			if pointsCalls == 1 {
				// The collection vanished between the cache fill and this upsert.
				exists = false
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"error": "Not found: Collection `docs` doesn't exist!"}})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": "ok"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := qdrant.New(srv.URL, "docs", "")
	err := c.Upsert(context.Background(), "550e8400-e29b-41d4-a716-446655440000",
		[]float32{0.1, 0.2, 0.3, 0.4}, nil, map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("expected recovery from 404, got error: %v", err)
	}
	if createCalls != 1 {
		t.Errorf("expected collection to be recreated exactly once, got %d", createCalls)
	}
	if pointsCalls != 2 {
		t.Errorf("expected upsert to be retried once (2 calls), got %d", pointsCalls)
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
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/collections/docs/points/delete" {
			json.NewDecoder(r.Body).Decode(&gotBody)
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": "acknowledged"}})
		} else {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := qdrant.New(srv.URL, "docs", "")
	if err := c.DeleteByDocID(context.Background(), "550e8400-e29b-41d4-a716-446655440000"); err != nil {
		t.Fatal(err)
	}
	// Verify filter-based delete body (not point-ID list).
	if _, hasFilter := gotBody["filter"]; !hasFilter {
		t.Errorf("expected filter-based delete body, got: %v", gotBody)
	}
}

func TestSearch_CollectionMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := qdrant.New(srv.URL, "docs", "")
	results, err := c.Search(context.Background(), "", []float32{0.1, 0.2}, 5)
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
	results, err := c.Search(context.Background(), "", []float32{0.1, 0.2}, 5)
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

// TestPayloadIndexes_CreatedOnEnsure verifies that configured payload fields are
// indexed when the collection is first ensured (here via Upsert), with Qdrant's
// real field-index request body, and only once even across multiple operations.
func TestPayloadIndexes_CreatedOnEnsure(t *testing.T) {
	var indexBodies []map[string]any
	created := false
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
		case r.Method == http.MethodPut && r.URL.Path == "/collections/docs/index":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			indexBodies = append(indexBodies, body)
			json.NewEncoder(w).Encode(map[string]any{"result": true})
		case r.Method == http.MethodPut && r.URL.Path == "/collections/docs/points":
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": "ok"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := qdrant.New(srv.URL, "docs", "")
	c.SetPayloadIndexFields([]string{"title", "tags"})

	// Two upserts: indexes must be created exactly once total (not per upsert).
	for i := 0; i < 2; i++ {
		if err := c.Upsert(context.Background(), "550e8400-e29b-41d4-a716-446655440000",
			[]float32{0.1, 0.2, 0.3, 0.4}, nil, map[string]any{}); err != nil {
			t.Fatal(err)
		}
	}

	if len(indexBodies) != 2 {
		t.Fatalf("expected one index call per field (2), got %d: %v", len(indexBodies), indexBodies)
	}
	if indexBodies[0]["field_name"] != "title" || indexBodies[0]["field_schema"] != "keyword" {
		t.Errorf("unexpected index body: %v", indexBodies[0])
	}
}

// TestPayloadIndexes_NoneConfigured verifies the index endpoint is never hit
// when no payload fields are configured.
func TestPayloadIndexes_NoneConfigured(t *testing.T) {
	indexCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections/docs":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && r.URL.Path == "/collections/docs":
			json.NewEncoder(w).Encode(map[string]any{"result": true})
		case r.Method == http.MethodPut && r.URL.Path == "/collections/docs/index":
			indexCalled = true
			json.NewEncoder(w).Encode(map[string]any{"result": true})
		case r.Method == http.MethodPut && r.URL.Path == "/collections/docs/points":
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": "ok"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := qdrant.New(srv.URL, "docs", "")
	if err := c.Upsert(context.Background(), "550e8400-e29b-41d4-a716-446655440000",
		[]float32{0.1, 0.2, 0.3, 0.4}, nil, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if indexCalled {
		t.Error("index endpoint should not be called when no payload fields are configured")
	}
}

func TestUpsert_HNSWConfig(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections/docs":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && r.URL.Path == "/collections/docs":
			json.NewDecoder(r.Body).Decode(&gotBody)
			json.NewEncoder(w).Encode(map[string]any{"result": true})
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

	// Verify HNSW config is included in the vector definition
	vectors, ok := gotBody["vectors"].(map[string]any)
	if !ok {
		t.Fatalf("expected vectors in body, got: %v", gotBody)
	}
	textVec, ok := vectors["text"].(map[string]any)
	if !ok {
		t.Fatalf("expected text vector, got: %v", vectors)
	}
	if _, hasHNSW := textVec["hnsw_config"]; !hasHNSW {
		t.Errorf("expected hnsw_config in vector definition, got: %v", textVec)
	}
}
