package opensearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

func (c *Client) Index(ctx context.Context, doc port.IndexDoc) error {
	payload := map[string]any{
		"doc_id":     doc.DocID,
		"title":      doc.Title,
		"series":     doc.Series,
		"content":    doc.Content,
		"summary":    doc.Summary,
		"tags":       doc.Tags,
		"date_month": doc.DateMonth,
		"stage":      doc.Stage,
		"status":     doc.Status,
	}
	b, status, err := c.do(ctx, http.MethodPut, "/"+c.index+"/_doc/"+doc.DocID, payload)
	if err != nil {
		return fmt.Errorf("opensearch index: %w", err)
	}
	if status >= 400 {
		return fmt.Errorf("opensearch index HTTP %d: %s", status, b)
	}
	return nil
}

func (c *Client) Delete(ctx context.Context, docID string) error {
	b, status, err := c.do(ctx, http.MethodDelete, "/"+c.index+"/_doc/"+docID, nil)
	if err != nil {
		return fmt.Errorf("opensearch delete: %w", err)
	}
	if status >= 400 && status != http.StatusNotFound {
		return fmt.Errorf("opensearch delete HTTP %d: %s", status, b)
	}
	return nil
}

func (c *Client) Count(ctx context.Context) (int, error) {
	b, status, err := c.do(ctx, http.MethodGet, "/"+c.index+"/_count", nil)
	if err != nil {
		return 0, fmt.Errorf("opensearch count: %w", err)
	}
	if status == http.StatusNotFound {
		return 0, nil
	}
	if status >= 400 {
		return 0, fmt.Errorf("opensearch count HTTP %d: %s", status, b)
	}
	var result struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		return 0, fmt.Errorf("opensearch count decode: %w", err)
	}
	return result.Count, nil
}
