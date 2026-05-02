package qdrant

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// Upsert inserts or updates a point in the Qdrant collection.
// If imageVector is non-empty and the collection uses named vectors, it is stored
// as the "image" named vector alongside the "text" vector. The chunk text in
// payload[PayloadText] is also tokenized into a BM25 sparse vector when the
// collection supports sparse vectors, enabling hybrid search.
func (c *Client) Upsert(ctx context.Context, id string, textVector []float32, imageVector []float32, payload map[string]any) error {
	imageLen := 0
	if len(imageVector) > 0 {
		imageLen = len(imageVector)
	}
	if _, err := c.ensureCollection(ctx, len(textVector), imageLen); err != nil {
		return err
	}
	feats := c.collectionFeatures(ctx)

	var pointVector any
	if feats.namedVectors {
		v := map[string]any{vectorNameText: textVector}
		if len(imageVector) > 0 {
			v[vectorNameImage] = imageVector
		} else {
			slog.Debug("named-vector collection; upserting text vector only", "collection", c.collection)
		}
		if feats.hasSparse {
			if text, _ := payload[port.PayloadText].(string); text != "" {
				if sv := BM25Vector(text); len(sv.Indices) > 0 {
					v[sparseNameText] = sv
				}
			}
		}
		pointVector = v
	} else {
		if len(imageVector) > 0 {
			slog.Warn("embed_image=true but collection uses unnamed vectors — image vector skipped", "collection", c.collection)
		}
		pointVector = textVector
	}

	body := map[string]any{
		"points": []map[string]any{
			{"id": idFromUUID(id), "vector": pointVector, "payload": payload},
		},
	}
	resp, err := c.do(ctx, http.MethodPut, "/collections/"+c.collection+"/points", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("qdrant: upsert %d: %s", resp.StatusCode, readBody(resp.Body))
	}
	slog.Info("qdrant upsert ok", "id", id[:8])
	return nil
}

// DeleteByDocID removes all chunk points for a document using a payload filter.
func (c *Client) DeleteByDocID(ctx context.Context, docID string) error {
	return c.deleteByPayload(ctx, "doc_id", docID)
}

// DeleteBySeries removes all series corpus points using a payload filter.
func (c *Client) DeleteBySeries(ctx context.Context, series string) error {
	return c.deleteByPayload(ctx, "series_name", series)
}

func (c *Client) deleteByPayload(ctx context.Context, key, value string) error {
	body := map[string]any{
		"filter": map[string]any{
			"must": []map[string]any{
				{"key": key, "match": map[string]any{"value": value}},
			},
		},
	}
	resp, err := c.do(ctx, http.MethodPost, "/collections/"+c.collection+"/points/delete", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("qdrant: delete by %s %d: %s", key, resp.StatusCode, readBody(resp.Body))
	}
	slog.Info("qdrant delete ok", "key", key, "value", value)
	return nil
}

// Search returns the top-k matches for the query. When the collection has
// sparse vectors configured, it performs hybrid retrieval (dense vector + BM25
// sparse) fused via Reciprocal Rank Fusion. Otherwise it falls back to plain
// dense search. queryText is used to build the sparse half of the hybrid query;
// pass "" to skip sparse and force dense-only.
func (c *Client) Search(ctx context.Context, queryText string, vector []float32, topK int) ([]port.EmbedResult, error) {
	feats := c.collectionFeatures(ctx)
	if !feats.exists {
		return nil, nil
	}
	hybrid := feats.namedVectors && feats.hasSparse && queryText != ""

	var (
		path string
		body map[string]any
	)
	if hybrid {
		sv := BM25Vector(queryText)
		path = "/collections/" + c.collection + "/points/query"
		body = map[string]any{
			"prefetch": []map[string]any{
				{"query": vector, "using": vectorNameText, "limit": hybridPrefetchK},
				{"query": map[string]any{"indices": sv.Indices, "values": sv.Values}, "using": sparseNameText, "limit": hybridPrefetchK},
			},
			"query":        map[string]any{"fusion": rrfFusion},
			"limit":        topK,
			"with_payload": true,
		}
	} else {
		path = "/collections/" + c.collection + "/points/search"
		var searchVector any
		if feats.namedVectors {
			searchVector = map[string]any{"name": vectorNameText, "vector": vector}
		} else {
			searchVector = vector
		}
		body = map[string]any{
			"vector":       searchVector,
			"limit":        topK,
			"with_payload": true,
		}
	}

	sresp, err := c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	defer sresp.Body.Close()
	if sresp.StatusCode >= 300 {
		slog.Error("qdrant search error", "status", sresp.StatusCode, "body", readBody(sresp.Body), "hybrid", hybrid)
		return nil, nil
	}

	// /points/search returns "result": [hit, ...]
	// /points/query returns "result": {"points": [hit, ...]}
	type hit struct {
		ID      uint64         `json:"id"`
		Score   float64        `json:"score"`
		Payload map[string]any `json:"payload"`
	}
	var raw struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(sresp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("qdrant: decode search response: %w", err)
	}
	var hits []hit
	if hybrid {
		var w struct {
			Points []hit `json:"points"`
		}
		if err := json.Unmarshal(raw.Result, &w); err != nil {
			return nil, fmt.Errorf("qdrant: decode query response: %w", err)
		}
		hits = w.Points
	} else {
		if err := json.Unmarshal(raw.Result, &hits); err != nil {
			return nil, fmt.Errorf("qdrant: decode search response: %w", err)
		}
	}

	results := make([]port.EmbedResult, 0, len(hits))
	for _, h := range hits {
		results = append(results, port.EmbedResult{
			ID:      fmt.Sprintf("%d", h.ID),
			Score:   h.Score,
			Payload: h.Payload,
		})
	}
	return results, nil
}

// GetByIDs fetches specific points by their string chunk IDs. Returned results
// have ID set to the original string chunk ID (not the Qdrant numeric ID) so
// callers can index results by the same IDs they requested.
func (c *Client) GetByIDs(ctx context.Context, ids []string) ([]port.EmbedResult, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	numericToString := make(map[uint64]string, len(ids))
	numericIDs := make([]uint64, len(ids))
	for i, id := range ids {
		n := idFromUUID(id)
		numericIDs[i] = n
		numericToString[n] = id
	}
	body := map[string]any{
		"ids":          numericIDs,
		"with_payload": true,
	}
	resp, err := c.do(ctx, http.MethodPost, "/collections/"+c.collection+"/points", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, nil
	}
	var out struct {
		Result []struct {
			ID      uint64         `json:"id"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("qdrant: decode points response: %w", err)
	}
	results := make([]port.EmbedResult, 0, len(out.Result))
	for _, h := range out.Result {
		results = append(results, port.EmbedResult{
			ID:      numericToString[h.ID],
			Payload: h.Payload,
		})
	}
	return results, nil
}
