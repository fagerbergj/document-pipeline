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

// ChatWithTools calls /api/chat with optional tool definitions (non-streaming).
// Returns (responseText, toolCalls, error). If the model requests tool calls,
// responseText is empty; the caller should execute the tools and call again.
func (c *Client) ChatWithTools(ctx context.Context, model string, messages []port.LLMMessage, tools []port.LLMTool) (string, []port.LLMToolCall, error) {
	msgs := msgsToOllama(messages)

	payload := map[string]any{
		"model":    model,
		"messages": msgs,
		"stream":   false,
	}
	if len(tools) > 0 {
		ollamaTools := make([]map[string]any, len(tools))
		for i, t := range tools {
			ollamaTools[i] = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Parameters,
				},
			}
		}
		payload["tools"] = ollamaTools
	}

	body, err := jsonPost(ctx, c.httpLong, c.baseURL+"/api/chat", payload)
	if err != nil {
		return "", nil, fmt.Errorf("ollama chat-with-tools: %w", err)
	}

	var resp struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string         `json:"name"`
					Arguments map[string]any `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", nil, fmt.Errorf("ollama chat-with-tools decode: %w", err)
	}

	if len(resp.Message.ToolCalls) > 0 {
		calls := make([]port.LLMToolCall, len(resp.Message.ToolCalls))
		for i, tc := range resp.Message.ToolCalls {
			calls[i] = port.LLMToolCall{
				ID:        fmt.Sprintf("call_%d", i),
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			}
		}
		return "", calls, nil
	}
	return resp.Message.Content, nil, nil
}

// msgsToOllama converts port.LLMMessage slice to the Ollama API message format.
func msgsToOllama(messages []port.LLMMessage) []map[string]any {
	msgs := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		msg := map[string]any{"role": m.Role, "content": m.Content}
		if len(m.Images) > 0 {
			imgs := make([]string, len(m.Images))
			for i, img := range m.Images {
				imgs[i] = base64.StdEncoding.EncodeToString(img)
			}
			msg["images"] = imgs
		}
		if len(m.ToolCalls) > 0 {
			tcs := make([]map[string]any, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				tcs[i] = map[string]any{
					"function": map[string]any{
						"name":      tc.Name,
						"arguments": tc.Arguments,
					},
				}
			}
			msg["tool_calls"] = tcs
		}
		msgs = append(msgs, msg)
	}
	return msgs
}

// ChatStream streams /api/chat, calling onChunk for each token.
func (c *Client) ChatStream(ctx context.Context, model string, messages []port.LLMMessage, onChunk func(string)) error {
	payload := map[string]any{
		"model":    model,
		"messages": msgsToOllama(messages),
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
