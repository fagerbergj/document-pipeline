package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a minimal OpenSearch HTTP client.
type Client struct {
	baseURL string
	index   string
	http    *http.Client
}

func NewClient(baseURL, index string) *Client {
	return &Client{
		baseURL: baseURL,
		index:   index,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) EnsureIndex(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, c.baseURL+"/"+c.index, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("opensearch check index: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}

	mapping := map[string]any{
		"mappings": map[string]any{
			"properties": map[string]any{
				"doc_id":     map[string]any{"type": "keyword"},
				"title":      map[string]any{"type": "text"},
				"series":     map[string]any{"type": "keyword"},
				"content":    map[string]any{"type": "text"},
				"summary":    map[string]any{"type": "text"},
				"tags":       map[string]any{"type": "keyword"},
				"date_month": map[string]any{"type": "keyword"},
				"stage":      map[string]any{"type": "keyword"},
				"status":     map[string]any{"type": "keyword"},
			},
		},
	}
	body, _ := json.Marshal(mapping)
	req, _ = http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/"+c.index, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err = c.http.Do(req)
	if err != nil {
		return fmt.Errorf("opensearch create index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("opensearch create index HTTP %d: %s", resp.StatusCode, b)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, payload any) ([]byte, int, error) {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, 0, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return b, resp.StatusCode, nil
}
