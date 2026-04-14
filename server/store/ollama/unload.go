package ollama

import (
	"context"
	"log/slog"
)

// Unload sends keep_alive=0 to evict the model from GPU memory.
// Errors are logged but not returned — unloading is best-effort.
func (c *Client) Unload(ctx context.Context, model string) error {
	payload := map[string]any{
		"model":      model,
		"keep_alive": 0,
	}
	_, err := jsonPost(ctx, c.httpShort, c.baseURL+"/api/generate", payload)
	if err != nil {
		slog.Warn("failed to unload model", "model", model, "err", err)
	} else {
		slog.Info("unloaded model", "model", model)
	}
	return nil
}
