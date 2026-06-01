// Package qdrant is an HTTP client for the Qdrant vector database.
// It is used by store/embed.EmbedStoreCoordinator and is not a port itself.
package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
)

const (
	vectorNameText  = "text"
	vectorNameImage = "image"
	sparseNameText  = "text_sparse"
	distanceCosine  = "Cosine"
	idfModifier     = "idf"
	rrfFusion       = "rrf"
	hybridPrefetchK = 30 // candidates per branch before RRF fusion
)

// Client talks to a Qdrant instance over HTTP.
type Client struct {
	baseURL    string
	collection string
	apiKey     string
	http       *http.Client

	// Collection capabilities are fetched once on first need and cached. The
	// schema doesn't change after creation in this codebase, so a cache miss
	// only happens on the very first upsert/search per process.
	featuresMu sync.RWMutex
	features   *collectionFeatures

	// payloadFields are the metadata keys to build payload indexes for. They are
	// indexed the first time the collection is touched (see ensureCollection),
	// not per upsert, and only after the collection exists with its real vector
	// dimensions. payloadIndexed guards against re-issuing the requests.
	payloadMu      sync.Mutex
	payloadFields  []string
	payloadIndexed bool
}

// SetPayloadIndexFields configures the metadata fields that get Qdrant payload
// indexes (for fast filtering during RAG). The indexes are created lazily the
// first time the collection is ensured. Call before the first upsert/search.
func (c *Client) SetPayloadIndexFields(fields []string) {
	c.payloadMu.Lock()
	c.payloadFields = fields
	c.payloadMu.Unlock()
}

// collectionFeatures captures the parts of a collection's config that affect
// how Upsert and Search build their request bodies.
type collectionFeatures struct {
	exists       bool
	namedVectors bool
	hasSparse    bool
}

func New(baseURL, collection, apiKey string) *Client {
	return &Client{
		baseURL:    baseURL,
		collection: collection,
		apiKey:     apiKey,
		http:       &http.Client{Timeout: 30 * 1e9},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("api-key", c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func readBody(r io.ReadCloser) string {
	b, _ := io.ReadAll(io.LimitReader(r, 512))
	r.Close()
	return string(b)
}

// collectionFeatures returns the cached capabilities of the collection,
// fetching once on first call. Returns exists=false when the collection
// is missing — callers can use that to short-circuit search.
func (c *Client) collectionFeatures(ctx context.Context) collectionFeatures {
	c.featuresMu.RLock()
	if f := c.features; f != nil {
		c.featuresMu.RUnlock()
		return *f
	}
	c.featuresMu.RUnlock()

	c.featuresMu.Lock()
	defer c.featuresMu.Unlock()
	if c.features != nil {
		return *c.features
	}
	f := c.fetchFeatures(ctx)
	c.features = &f
	return f
}

func (c *Client) setFeatures(f collectionFeatures) {
	c.featuresMu.Lock()
	c.features = &f
	c.featuresMu.Unlock()
}

// invalidateFeatures drops the cached collection capabilities so the next
// collectionFeatures call re-fetches them. Used when we learn the collection
// changed underneath us — e.g. it was deleted out of band by a re-embed run,
// leaving our cached exists=true stale.
func (c *Client) invalidateFeatures() {
	c.featuresMu.Lock()
	c.features = nil
	c.featuresMu.Unlock()
	// A recreated collection starts with no payload indexes, so allow them to be
	// rebuilt on the next ensureCollection rather than staying marked as done.
	c.payloadMu.Lock()
	c.payloadIndexed = false
	c.payloadMu.Unlock()
}

// fetchFeatures issues a single GET /collections/<name> and parses both the
// vectors and sparse_vectors config. Errors and 404s collapse to a zero
// features value (exists=false).
func (c *Client) fetchFeatures(ctx context.Context) collectionFeatures {
	resp, err := c.do(ctx, http.MethodGet, "/collections/"+c.collection, nil)
	if err != nil {
		return collectionFeatures{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return collectionFeatures{}
	}
	var out struct {
		Result struct {
			Config struct {
				Params struct {
					Vectors       json.RawMessage            `json:"vectors"`
					SparseVectors map[string]json.RawMessage `json:"sparse_vectors"`
				} `json:"params"`
			} `json:"config"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return collectionFeatures{}
	}
	// Named: {"text": {...}, "image": {...}}  Unnamed: {"size": N, ...}
	var dense map[string]json.RawMessage
	if err := json.Unmarshal(out.Result.Config.Params.Vectors, &dense); err != nil {
		return collectionFeatures{exists: true}
	}
	_, hasSize := dense["size"]
	_, hasSparse := out.Result.Config.Params.SparseVectors[sparseNameText]
	return collectionFeatures{
		exists:       true,
		namedVectors: !hasSize,
		hasSparse:    hasSparse,
	}
}

// ensureCollection creates the collection if it does not exist and caches its
// features. New collections are always created with named vectors
// (text + optional image) plus a named sparse vector ("text_sparse") with the
// IDF modifier so hybrid search works.
func (c *Client) ensureCollection(ctx context.Context, textLen int, imageLen int) (named bool, err error) {
	if f := c.collectionFeatures(ctx); f.exists {
		c.ensurePayloadIndexes(ctx)
		return f.namedVectors, nil
	}

	dense := map[string]any{
		vectorNameText: map[string]any{"size": textLen, "distance": distanceCosine},
	}
	if imageLen > 0 {
		dense[vectorNameImage] = map[string]any{"size": imageLen, "distance": distanceCosine}
	}
	sparse := map[string]any{
		sparseNameText: map[string]any{"modifier": idfModifier},
	}

	// Add HNSW indexing parameters for efficient similarity search
	// ef_construction: higher = better recall but slower index building
	// M: higher = better recall but more memory
	hnswConfig := map[string]any{
		"ef_construct": 100,
		"m":            16,
	}
	dense[vectorNameText].(map[string]any)["hnsw_config"] = hnswConfig
	if imageLen > 0 {
		dense[vectorNameImage].(map[string]any)["hnsw_config"] = hnswConfig
	}

	cr, err := c.do(ctx, http.MethodPut, "/collections/"+c.collection, map[string]any{
		"vectors":        dense,
		"sparse_vectors": sparse,
	})
	if err != nil {
		return false, err
	}
	if cr.StatusCode >= 300 {
		body := readBody(cr.Body)
		return false, fmt.Errorf("qdrant: create collection %d: %s", cr.StatusCode, body)
	}
	cr.Body.Close()
	c.setFeatures(collectionFeatures{exists: true, namedVectors: true, hasSparse: true})
	slog.Info("created qdrant collection", "collection", c.collection, "with_sparse", true, "hnsw_ef_construct", 100, "hnsw_m", 16)
	c.ensurePayloadIndexes(ctx)
	return true, nil
}

// idFromUUID converts a UUID string to a stable uint63 for Qdrant point IDs.
func idFromUUID(id string) uint64 {
	var h uint64
	for _, ch := range id {
		if ch == '-' {
			continue
		}
		h = h*16 + uint64(hexVal(byte(ch)))
	}
	return h % (1 << 63)
}

func hexVal(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10
	}
	return 0
}

// ensurePayloadIndexes builds a keyword payload index for each configured field
// (see SetPayloadIndexFields), enabling fast filtering on metadata like title,
// tags, or summary. It runs at most once per process and must be called only
// after the collection exists with its real vector dimensions — callers are the
// two ensureCollection return paths. Index creation is idempotent in Qdrant, so
// fields already indexed are no-ops; failures are logged and retried on the next
// call (payloadIndexed stays false) since indexes are an optimization, not a
// correctness requirement for upsert.
func (c *Client) ensurePayloadIndexes(ctx context.Context) {
	c.payloadMu.Lock()
	defer c.payloadMu.Unlock()
	if c.payloadIndexed || len(c.payloadFields) == 0 {
		return
	}
	allOK := true
	for _, field := range c.payloadFields {
		if err := c.createPayloadIndex(ctx, field); err != nil {
			slog.Warn("failed to create qdrant payload index", "field", field, "err", err)
			allOK = false
		}
	}
	c.payloadIndexed = allOK
}

// createPayloadIndex issues Qdrant's "create field index" request for a single
// field. The schema is always "keyword", which suits the text metadata we index
// (title, tags, summary, date_month); a numeric/geo field would need a different
// schema and Qdrant would reject "keyword" for it. Unlike the rest of do()'s
// callers we must inspect the HTTP status: do() only returns an error on
// transport failure, so a 4xx (e.g. a malformed request) would otherwise be
// silently treated as success.
func (c *Client) createPayloadIndex(ctx context.Context, field string) error {
	resp, err := c.do(ctx, http.MethodPut, "/collections/"+c.collection+"/index", map[string]any{
		"field_name":   field,
		"field_schema": "keyword",
	})
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("qdrant: create payload index %q: %d: %s", field, resp.StatusCode, readBody(resp.Body))
	}
	resp.Body.Close()
	slog.Debug("ensured qdrant payload index", "field", field)
	return nil
}
