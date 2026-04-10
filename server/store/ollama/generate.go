package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// GenerateVision calls /api/generate with stream:false (vision models are slow
// to first token; streaming adds no value here). onChunk is called once with
// the full response.
func (c *Client) GenerateVision(ctx context.Context, model, prompt string, imageBytes []byte, onChunk func(string)) error {
	payload := map[string]any{
		"model":  model,
		"prompt": prompt,
		"images": []string{base64.StdEncoding.EncodeToString(imageBytes)},
		"stream": false,
	}
	body, err := jsonPost(ctx, c.httpLong, c.baseURL+"/api/generate", payload)
	if err != nil {
		return fmt.Errorf("ollama generate vision: %w", err)
	}
	var resp struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("ollama generate vision decode: %w", err)
	}
	if onChunk != nil && resp.Response != "" {
		onChunk(resp.Response)
	}
	return nil
}

// GenerateText streams /api/generate, calling onChunk for each token.
func (c *Client) GenerateText(ctx context.Context, model, prompt string, onChunk func(string)) error {
	payload := map[string]any{
		"model":  model,
		"prompt": prompt,
		"stream": true,
	}
	return streamGenerate(ctx, c.httpLong, c.baseURL+"/api/generate", payload, func(line []byte) error {
		var chunk struct {
			Response string `json:"response"`
			Done     bool   `json:"done"`
		}
		if err := json.Unmarshal(line, &chunk); err != nil {
			return err
		}
		if onChunk != nil && chunk.Response != "" {
			onChunk(chunk.Response)
		}
		if chunk.Done {
			return io.EOF
		}
		return nil
	})
}

// ChatStream streams /api/chat, calling onChunk for each token.
func (c *Client) ChatStream(ctx context.Context, model string, messages []port.LLMMessage, onChunk func(string)) error {
	msgs := make([]map[string]string, len(messages))
	for i, m := range messages {
		msgs[i] = map[string]string{"role": m.Role, "content": m.Content}
	}
	payload := map[string]any{
		"model":    model,
		"messages": msgs,
		"stream":   true,
	}
	return streamGenerate(ctx, c.httpLong, c.baseURL+"/api/chat", payload, func(line []byte) error {
		var chunk struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done bool `json:"done"`
		}
		if err := json.Unmarshal(line, &chunk); err != nil {
			return err
		}
		if onChunk != nil && chunk.Message.Content != "" {
			onChunk(chunk.Message.Content)
		}
		if chunk.Done {
			return io.EOF
		}
		return nil
	})
}

// streamGenerate POSTs payload and reads NDJSON lines, calling onLine for each.
// onLine should return io.EOF to signal clean completion, or any other error to abort.
func streamGenerate(ctx context.Context, client *http.Client, url string, payload map[string]any, onLine func([]byte) error) error {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		slog.Error("ollama error", "status", resp.StatusCode, "body", string(body))
		return fmt.Errorf("ollama HTTP %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if err := onLine(line); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
	}
	return scanner.Err()
}

// jsonPost sends a JSON POST and returns the full response body.
func jsonPost(ctx context.Context, client *http.Client, url string, payload map[string]any) ([]byte, error) {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		slog.Error("ollama error", "status", resp.StatusCode, "body", string(body[:min(len(body), 512)]))
		return nil, fmt.Errorf("ollama HTTP %d", resp.StatusCode)
	}
	return body, nil
}
