package vllm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// ChatWithTools calls /v1/chat/completions and maps the OpenAI-style response
// onto port.LLMChatResponse. Reasoning content (when the server is launched
// with --reasoning-parser) is surfaced as Thinking; tool calls are returned
// with their IDs preserved so the caller can echo them back as tool_call_id
// on the next turn.
func (c *Client) ChatWithTools(ctx context.Context, model string, messages []port.LLMMessage, tools []port.LLMTool) (port.LLMChatResponse, error) {
	payload := map[string]any{
		"model":    model,
		"messages": msgsToOpenAI(messages),
		"stream":   false,
		// AWQ-Llama3 parallel tool calls silently drop the second call in
		// vllm's llama3_json parser. Most agent loops also handle one call at
		// a time, so opt out across the board.
		"parallel_tool_calls": false,
	}
	if len(tools) > 0 {
		toolsArr := make([]map[string]any, len(tools))
		for i, t := range tools {
			toolsArr[i] = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Parameters,
				},
			}
		}
		payload["tools"] = toolsArr
		payload["tool_choice"] = "auto"
	}

	body, err := c.jsonPost(ctx, "/v1/chat/completions", payload)
	if err != nil {
		if b, jerr := json.Marshal(payload); jerr == nil {
			slog.Debug("vllm chat-with-tools failed", "payload", string(b))
		}
		return port.LLMChatResponse{}, fmt.Errorf("vllm chat-with-tools: %w", err)
	}

	var resp chatCompletionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return port.LLMChatResponse{}, fmt.Errorf("vllm chat-with-tools decode: %w", err)
	}
	if len(resp.Choices) == 0 {
		return port.LLMChatResponse{}, errors.New("vllm chat-with-tools: empty choices")
	}
	msg := resp.Choices[0].Message

	out := port.LLMChatResponse{
		Text:     msg.Content,
		Thinking: msg.reasoning(),
	}
	if len(msg.ToolCalls) > 0 {
		out.ToolCalls = make([]port.LLMToolCall, 0, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			args, perr := parseToolArguments(tc.Function.Arguments)
			if perr != nil {
				slog.Warn("vllm tool-call args not valid JSON; passing as raw string", "name", tc.Function.Name, "err", perr)
				args = map[string]any{"_raw": tc.Function.Arguments}
			}
			id := tc.ID
			if id == "" {
				id = fmt.Sprintf("call_%d", i)
			}
			out.ToolCalls = append(out.ToolCalls, port.LLMToolCall{
				ID:        id,
				Name:      tc.Function.Name,
				Arguments: args,
			})
		}
	}
	return out, nil
}

// ChatStream streams /v1/chat/completions with stream:true. Only the content
// delta is forwarded — tool-call deltas and reasoning deltas are not
// surfaced through this method (use ChatWithTools for those).
func (c *Client) ChatStream(ctx context.Context, model string, messages []port.LLMMessage, onChunk func(string)) error {
	payload := map[string]any{
		"model":    model,
		"messages": msgsToOpenAI(messages),
		"stream":   true,
	}
	return c.streamSSE(ctx, "/v1/chat/completions", payload, func(data []byte) error {
		var chunk chatCompletionChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return err
		}
		if len(chunk.Choices) == 0 {
			return nil
		}
		if txt := chunk.Choices[0].Delta.Content; txt != "" && onChunk != nil {
			onChunk(txt)
		}
		return nil
	})
}

// GenerateText sends prompt as a single user message to /v1/chat/completions
// (vLLM's /v1/completions exists but uses legacy completion semantics most
// modern instruction models aren't tuned for; chat with a one-shot user
// message is the better default).
func (c *Client) GenerateText(ctx context.Context, model, prompt string, onChunk func(string)) error {
	return c.ChatStream(ctx, model, []port.LLMMessage{
		{Role: "user", Content: prompt},
	}, onChunk)
}

// GenerateVision sends a single multimodal user turn with one image
// attachment. Returns the full assistant text in one onChunk call (vision
// generations are short and we don't gain meaningful UX from streaming them).
func (c *Client) GenerateVision(ctx context.Context, model, prompt string, imageBytes []byte, onChunk func(string)) error {
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes)
	payload := map[string]any{
		"model": model,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": prompt},
				{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
			},
		}},
		"stream": false,
	}

	body, err := c.jsonPost(ctx, "/v1/chat/completions", payload)
	if err != nil {
		return fmt.Errorf("vllm generate vision: %w", err)
	}
	var resp chatCompletionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("vllm generate vision decode: %w", err)
	}
	if len(resp.Choices) == 0 {
		return errors.New("vllm generate vision: empty choices")
	}
	if onChunk != nil && resp.Choices[0].Message.Content != "" {
		onChunk(resp.Choices[0].Message.Content)
	}
	return nil
}

// GenerateEmbed is not supported. vLLM serves one model per process; the
// chat process cannot also produce embeddings. Run a separate embedding
// service (a second vllm process with --task embed, or keep ollama for
// embeddings) and route /embed calls there.
func (c *Client) GenerateEmbed(_ context.Context, _, _ string) ([]float32, error) {
	return nil, errors.New("vllm backend does not support embeddings; use a dedicated embedding service")
}

// Unload is a no-op. vLLM holds its model in VRAM for the lifetime of the
// process; there is no per-request load/unload knob.
func (c *Client) Unload(_ context.Context, _ string) error { return nil }

// ── wire types ───────────────────────────────────────────────────────────────

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

type chatMessage struct {
	Role             string         `json:"role"`
	Content          string         `json:"content"`
	Reasoning        string         `json:"reasoning"`
	ReasoningContent string         `json:"reasoning_content"` // legacy field name in older vllm builds
	ToolCalls        []chatToolCall `json:"tool_calls"`
}

// reasoning returns whichever reasoning field the server populated. vLLM
// renamed reasoning_content → reasoning at some point; both still appear in
// the wild depending on server version.
func (m chatMessage) reasoning() string {
	if m.Reasoning != "" {
		return m.Reasoning
	}
	return m.ReasoningContent
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON-encoded string per OpenAI spec
	} `json:"function"`
}

type chatCompletionChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

// ── message conversion ───────────────────────────────────────────────────────

func msgsToOpenAI(messages []port.LLMMessage) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		msg := map[string]any{"role": m.Role}
		switch {
		case m.Role == "tool":
			// Tool responses carry the call id and the result as plain content.
			msg["tool_call_id"] = m.ToolCallID
			msg["content"] = m.Content
		case len(m.Images) > 0:
			parts := make([]map[string]any, 0, len(m.Images)+1)
			if m.Content != "" {
				parts = append(parts, map[string]any{"type": "text", "text": m.Content})
			}
			for _, img := range m.Images {
				dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(img)
				parts = append(parts, map[string]any{
					"type":      "image_url",
					"image_url": map[string]any{"url": dataURL},
				})
			}
			msg["content"] = parts
		default:
			msg["content"] = m.Content
		}
		if len(m.ToolCalls) > 0 {
			calls := make([]map[string]any, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				argsJSON, _ := json.Marshal(tc.Arguments)
				calls[i] = map[string]any{
					"id":   tc.ID,
					"type": "function",
					"function": map[string]any{
						"name":      tc.Name,
						"arguments": string(argsJSON),
					},
				}
			}
			msg["tool_calls"] = calls
		}
		out = append(out, msg)
	}
	return out
}

// parseToolArguments handles OpenAI's stringified-JSON arguments field. AWQ
// llama-3 occasionally double-encodes arrays — tolerate that by attempting a
// second decode when the first yields a string.
func parseToolArguments(s string) (map[string]any, error) {
	if strings.TrimSpace(s) == "" {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(s), &args); err != nil {
		return nil, err
	}
	for k, v := range args {
		if str, ok := v.(string); ok && looksLikeJSON(str) {
			var nested any
			if json.Unmarshal([]byte(str), &nested) == nil {
				args[k] = nested
			}
		}
	}
	return args, nil
}

func looksLikeJSON(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return false
	}
	return (s[0] == '[' && s[len(s)-1] == ']') || (s[0] == '{' && s[len(s)-1] == '}')
}

// ── HTTP helpers ─────────────────────────────────────────────────────────────

func (c *Client) jsonPost(ctx context.Context, path string, payload map[string]any) ([]byte, error) {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpLong.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		slog.Error("vllm error", "status", resp.StatusCode, "body", string(body[:min(len(body), 512)]))
		return nil, fmt.Errorf("vllm HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// streamSSE POSTs payload and reads Server-Sent Events, calling onData for
// each `data:` payload (other than the terminal `[DONE]` sentinel).
func (c *Client) streamSSE(ctx context.Context, path string, payload map[string]any, onData func([]byte) error) error {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpLong.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		slog.Error("vllm error", "status", resp.StatusCode, "body", string(body))
		return fmt.Errorf("vllm HTTP %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		data := bytes.TrimPrefix(line, []byte("data: "))
		if bytes.Equal(data, []byte("[DONE]")) {
			return nil
		}
		if err := onData(data); err != nil {
			return err
		}
	}
	return scanner.Err()
}
