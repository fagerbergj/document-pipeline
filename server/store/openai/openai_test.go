package openai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/fagerbergj/document-pipeline/server/store/openai"
)

func newClient(t *testing.T, mux *http.ServeMux) *openai.Client {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return openai.New(srv.URL, "")
}

// ── ChatWithTools ────────────────────────────────────────────────────────────

func TestChatWithTools_TextOnly(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["stream"] != false {
			t.Error("ChatWithTools must not stream")
		}
		if req["parallel_tool_calls"] != false {
			t.Error("parallel_tool_calls should be disabled")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"role":    "assistant",
					"content": "hello there",
				},
			}},
		})
	})

	c := newClient(t, mux)
	out, err := c.ChatWithTools(context.Background(), "llama-70b", []port.LLMMessage{
		{Role: "user", Content: "hi"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "hello there" {
		t.Errorf("Text = %q, want %q", out.Text, "hello there")
	}
	if out.Thinking != "" || len(out.ToolCalls) != 0 {
		t.Errorf("unexpected extras: %+v", out)
	}
}

func TestChatWithTools_Reasoning(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"role":      "assistant",
					"content":   "answer",
					"reasoning": "step by step thinking",
				},
			}},
		})
	})

	c := newClient(t, mux)
	out, _ := c.ChatWithTools(context.Background(), "qwen", []port.LLMMessage{{Role: "user", Content: "q"}}, nil)
	if out.Text != "answer" || out.Thinking != "step by step thinking" {
		t.Errorf("got %+v", out)
	}
}

func TestChatWithTools_ReasoningLegacyField(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"role":              "assistant",
					"content":           "answer",
					"reasoning_content": "legacy field name",
				},
			}},
		})
	})

	c := newClient(t, mux)
	out, _ := c.ChatWithTools(context.Background(), "qwen", []port.LLMMessage{{Role: "user", Content: "q"}}, nil)
	if out.Thinking != "legacy field name" {
		t.Errorf("Thinking = %q, want legacy field name", out.Thinking)
	}
}

func TestChatWithTools_ToolCall(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		tools, ok := req["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Errorf("expected one tool definition, got %v", req["tools"])
		}
		if req["tool_choice"] != "auto" {
			t.Errorf("tool_choice = %v, want auto", req["tool_choice"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"role":    "assistant",
					"content": "",
					"tool_calls": []map[string]any{{
						"id":   "call-xyz",
						"type": "function",
						"function": map[string]any{
							"name":      "rag_search",
							"arguments": `{"query":"foo","k":3}`,
						},
					}},
				},
			}},
		})
	})

	c := newClient(t, mux)
	out, err := c.ChatWithTools(context.Background(), "llama-70b",
		[]port.LLMMessage{{Role: "user", Content: "search"}},
		[]port.LLMTool{{Name: "rag_search", Description: "search the index", Parameters: map[string]any{"type": "object"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 1 {
		t.Fatalf("got %d calls, want 1", len(out.ToolCalls))
	}
	tc := out.ToolCalls[0]
	if tc.ID != "call-xyz" || tc.Name != "rag_search" {
		t.Errorf("call = %+v", tc)
	}
	if tc.Arguments["query"] != "foo" || tc.Arguments["k"] != float64(3) {
		t.Errorf("args = %+v", tc.Arguments)
	}
}

// AWQ Llama-3 sometimes double-encodes array args as a JSON string. The
// client should transparently decode the inner JSON so callers receive the
// natural shape.
func TestChatWithTools_StringifiedArrayArg(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"tool_calls": []map[string]any{{
						"id":   "c1",
						"type": "function",
						"function": map[string]any{
							"name":      "tag",
							"arguments": `{"tags":"[\"a\",\"b\"]"}`,
						},
					}},
				},
			}},
		})
	})

	c := newClient(t, mux)
	out, _ := c.ChatWithTools(context.Background(), "llama-70b",
		[]port.LLMMessage{{Role: "user", Content: "tag"}},
		[]port.LLMTool{{Name: "tag"}},
	)
	tags, ok := out.ToolCalls[0].Arguments["tags"].([]any)
	if !ok {
		t.Fatalf("tags arg not decoded to slice: %T %v", out.ToolCalls[0].Arguments["tags"], out.ToolCalls[0].Arguments["tags"])
	}
	if len(tags) != 2 || tags[0] != "a" {
		t.Errorf("tags = %v", tags)
	}
}

// Tool responses must be sent back with role=tool + tool_call_id so the
// server can pair them with the original call.
func TestChatWithTools_SendsToolResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		msgs := req["messages"].([]any)
		toolMsg := msgs[len(msgs)-1].(map[string]any)
		if toolMsg["role"] != "tool" {
			t.Errorf("expected last role tool, got %v", toolMsg["role"])
		}
		if toolMsg["tool_call_id"] != "call-abc" {
			t.Errorf("tool_call_id = %v", toolMsg["tool_call_id"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "ok"}}},
		})
	})

	c := newClient(t, mux)
	_, err := c.ChatWithTools(context.Background(), "llama-70b", []port.LLMMessage{
		{Role: "user", Content: "do thing"},
		{Role: "assistant", ToolCalls: []port.LLMToolCall{{ID: "call-abc", Name: "f", Arguments: map[string]any{"x": 1}}}},
		{Role: "tool", ToolCallID: "call-abc", Content: `{"result":"done"}`},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestChatWithTools_APIKey(t *testing.T) {
	mux := http.NewServeMux()
	var seenAuth string
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := openai.New(srv.URL, "secret-token")
	_, _ = c.ChatWithTools(context.Background(), "m", []port.LLMMessage{{Role: "user", Content: "hi"}}, nil)
	if seenAuth != "Bearer secret-token" {
		t.Errorf("auth header = %q", seenAuth)
	}
}

func TestChatWithTools_HTTPError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	})
	c := newClient(t, mux)
	_, err := c.ChatWithTools(context.Background(), "m", []port.LLMMessage{{Role: "user", Content: "x"}}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

// ── ChatStream ───────────────────────────────────────────────────────────────

func TestChatStream(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		writeSSE := func(obj any) {
			b, _ := json.Marshal(obj)
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
		writeSSE(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": "Hello"}}}})
		writeSSE(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": ", "}}}})
		writeSSE(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": "world"}}}})
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	})

	c := newClient(t, mux)
	var got strings.Builder
	err := c.ChatStream(context.Background(), "llama-70b", []port.LLMMessage{{Role: "user", Content: "hi"}}, func(s string) {
		got.WriteString(s)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "Hello, world" {
		t.Errorf("got %q", got.String())
	}
}

// ── GenerateVision ───────────────────────────────────────────────────────────

func TestGenerateVision_SendsMultimodalContent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		msgs := req["messages"].([]any)
		content := msgs[0].(map[string]any)["content"].([]any)
		if len(content) != 2 {
			t.Errorf("expected 2 content parts, got %d", len(content))
		}
		image := content[1].(map[string]any)
		if image["type"] != "image_url" {
			t.Errorf("part 1 type = %v", image["type"])
		}
		url := image["image_url"].(map[string]any)["url"].(string)
		if !strings.HasPrefix(url, "data:image/png;base64,") {
			t.Errorf("url not a data URL: %q", url[:min(40, len(url))])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "a cat"}}},
		})
	})

	c := newClient(t, mux)
	var got string
	err := c.GenerateVision(context.Background(), "vision-model", "what is this?", []byte("img"), func(s string) { got = s })
	if err != nil {
		t.Fatal(err)
	}
	if got != "a cat" {
		t.Errorf("got %q", got)
	}
}

// ── embeddings ───────────────────────────────────────────────────────────────

func TestGenerateEmbed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["model"] != "qwen3-embed" {
			t.Errorf("model: got %v", req["model"])
		}
		if req["input"] != "hello world" {
			t.Errorf("input: got %v", req["input"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{
				"embedding": []float32{0.1, 0.2, 0.3, 0.4},
			}},
		})
	})

	c := newClient(t, mux)
	vec, err := c.GenerateEmbed(context.Background(), "qwen3-embed", "hello world")
	if err != nil {
		t.Fatalf("GenerateEmbed: %v", err)
	}
	if len(vec) != 4 || vec[0] != 0.1 {
		t.Errorf("unexpected vector: %v", vec)
	}
}

func TestGenerateEmbed_EmptyData(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	})
	c := newClient(t, mux)
	if _, err := c.GenerateEmbed(context.Background(), "m", "x"); err == nil {
		t.Fatal("expected error on empty data")
	}
}
