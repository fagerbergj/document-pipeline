// Package openai implements port.LLMInference against any OpenAI-compatible
// HTTP server (llama.cpp / llama-swap, vLLM, LM Studio, etc.). It is the
// single LLM backend for chat, vision, plain-text generation, and embeddings;
// model selection per stage is done via per-request `model` fields rather
// than per-server.
package openai

import (
	"net/http"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// Client implements port.LLMInference against an OpenAI-compatible API.
//
// All surfaces ride through the standard endpoints: /v1/chat/completions for
// chat and vision, /v1/embeddings for embeddings. Model swapping is handled
// upstream (llama-swap routes by model name to per-model llama-server
// instances).
type Client struct {
	baseURL  string // e.g. "http://llm-swap:11436"
	apiKey   string // optional; sent as "Authorization: Bearer <key>"
	httpLong *http.Client
}

var _ port.LLMInference = (*Client)(nil)

// New returns an OpenAI-compatible client targeting baseURL (no trailing
// slash, no /v1 suffix — that's added per-request). apiKey is optional; pass
// "" for servers without auth (e.g. local llama-swap).
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL:  baseURL,
		apiKey:   apiKey,
		httpLong: &http.Client{Timeout: 600 * time.Second},
	}
}
