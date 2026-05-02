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
)

const (
	vectorNameText  = "text"
	vectorNameImage = "image"
	sparseNameText  = "text_sparse"
	distanceCosine  = "Cosine"
	hybridPrefetchK = 30 // candidates per branch before RRF fusion
)

// Client talks to a Qdrant instance over HTTP.
type Client struct {
	baseURL    string
	collection string
	apiKey     string
	http       *http.Client
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

// usesNamedVectors returns true when the collection was created with named
// vectors ("text" / "image") rather than a single unnamed vector.
func (c *Client) usesNamedVectors(ctx context.Context) bool {
	resp, err := c.do(ctx, http.MethodGet, "/collections/"+c.collection, nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return false
	}
	defer resp.Body.Close()
	var out struct {
		Result struct {
			Config struct {
				Params struct {
					Vectors json.RawMessage `json:"vectors"`
				} `json:"params"`
			} `json:"config"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false
	}
	// Named: {"text": {...}, "image": {...}}  Unnamed: {"size": N, ...}
	var named map[string]json.RawMessage
	if err := json.Unmarshal(out.Result.Config.Params.Vectors, &named); err != nil {
		return false
	}
	_, hasSize := named["size"]
	return !hasSize
}

// ensureCollection creates the collection if it does not exist.
// Returns whether the collection uses named vectors. New collections are
// always created with named vectors (text + optional image) plus a named
// sparse vector ("text_sparse") with IDF modifier so hybrid search works.
func (c *Client) ensureCollection(ctx context.Context, textLen int, imageLen int) (named bool, err error) {
	resp, err := c.do(ctx, http.MethodGet, "/collections/"+c.collection, nil)
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return c.usesNamedVectors(ctx), nil
	}
	if resp.StatusCode != http.StatusNotFound {
		return false, fmt.Errorf("qdrant: GET collection %d", resp.StatusCode)
	}

	// Create collection — always named to leave room for image + sparse.
	dense := map[string]any{
		vectorNameText: map[string]any{"size": textLen, "distance": distanceCosine},
	}
	if imageLen > 0 {
		dense[vectorNameImage] = map[string]any{"size": imageLen, "distance": distanceCosine}
	}
	sparse := map[string]any{
		sparseNameText: map[string]any{"modifier": "idf"},
	}
	named = true
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
	slog.Info("created qdrant collection", "collection", c.collection, "with_sparse", true)
	return named, nil
}

// hasSparseVectors reports whether the collection's config includes the
// "text_sparse" sparse vector. Older collections created before hybrid support
// won't have it; in that case the client falls back to dense-only search.
func (c *Client) hasSparseVectors(ctx context.Context) bool {
	resp, err := c.do(ctx, http.MethodGet, "/collections/"+c.collection, nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return false
	}
	defer resp.Body.Close()
	var out struct {
		Result struct {
			Config struct {
				Params struct {
					SparseVectors map[string]json.RawMessage `json:"sparse_vectors"`
				} `json:"params"`
			} `json:"config"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false
	}
	_, ok := out.Result.Config.Params.SparseVectors[sparseNameText]
	return ok
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
