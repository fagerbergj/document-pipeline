package opensearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

func (c *Client) Search(ctx context.Context, query string, size int) ([]string, error) {
	payload := map[string]any{
		"size": size,
		"query": map[string]any{
			"query_string": map[string]any{
				"query":            query,
				"fields":           []string{"title^3", "series^2", "summary^2", "tags^2", "content", "stage", "status"},
				"default_operator": "AND",
			},
		},
		"_source": []string{"doc_id"},
	}
	b, status, err := c.do(ctx, http.MethodPost, "/"+c.index+"/_search", payload)
	if err != nil {
		return nil, fmt.Errorf("opensearch search: %w", err)
	}
	if status >= 400 {
		return nil, fmt.Errorf("opensearch search HTTP %d: %s", status, b)
	}

	var result struct {
		Hits struct {
			Hits []struct {
				Source struct {
					DocID string `json:"doc_id"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, fmt.Errorf("opensearch search decode: %w", err)
	}

	ids := make([]string, 0, len(result.Hits.Hits))
	for _, h := range result.Hits.Hits {
		if h.Source.DocID != "" {
			ids = append(ids, h.Source.DocID)
		}
	}
	return ids, nil
}
