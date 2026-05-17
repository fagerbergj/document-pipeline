// Package vllm implements port.LLMInference against vLLM's OpenAI-compatible
// HTTP server. Intended as the chat backend; pipeline stages (OCR, clarify,
// classify, embed) continue to use the ollama store. vLLM's tensor parallelism
// makes it noticeably faster on multi-GPU setups for large chat models, but it
// only serves one model per process so it is not a drop-in replacement for
// ollama's per-stage model swapping.
package vllm

import (
	"net/http"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// Client implements port.LLMInference against the OpenAI-compatible vLLM API.
//
// Only the chat surface is implemented in depth. Vision rides through the
// multimodal chat-completions content array. Plain-text generation is
// delegated to chat with a single user message — vLLM does expose
// /v1/completions but we don't need its legacy semantics. Embedding is
// unsupported (vLLM serves one model per process; embedding deployment is a
// separate concern, so callers should keep using the ollama embed path).
type Client struct {
	baseURL  string // e.g. "http://vllm:8000"
	apiKey   string // optional; sent as "Authorization: Bearer <key>"
	httpLong *http.Client
}

var _ port.LLMInference = (*Client)(nil)

// New returns a vLLM client targeting baseURL (no trailing slash, no /v1
// suffix — that's added per-request). apiKey is optional; pass "" if the
// server was started without --api-key.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL:  baseURL,
		apiKey:   apiKey,
		httpLong: &http.Client{Timeout: 600 * time.Second},
	}
}
