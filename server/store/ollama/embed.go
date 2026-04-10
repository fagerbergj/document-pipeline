package ollama

import (
	"context"
	"encoding/json"
	"fmt"
)

// GenerateEmbed calls /api/embed and returns the first embedding vector.
func (c *Client) GenerateEmbed(ctx context.Context, model, text string) ([]float32, error) {
	if text == "" {
		text = " " // Ollama rejects empty input
	}
	payload := map[string]any{
		"model": model,
		"input": text,
	}
	body, err := jsonPost(ctx, c.httpEmbed, c.baseURL+"/api/embed", payload)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}

	// /api/embed returns {"embeddings": [[...]]} (batch API)
	var resp struct {
		Embeddings [][]float32 `json:"embeddings"`
		Embedding  []float32   `json:"embedding"` // older single-vector fallback
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("ollama embed decode: %w", err)
	}
	if len(resp.Embeddings) > 0 {
		return resp.Embeddings[0], nil
	}
	return resp.Embedding, nil
}
