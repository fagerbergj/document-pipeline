// Package openai implements port.LLMInference against any OpenAI-compatible
// HTTP server (llama-swap, vLLM, LM Studio, etc.).
package openai

import (
	"net/http"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// Client implements port.LLMInference against an OpenAI-compatible HTTP API.
type Client struct {
	baseURL  string // e.g. "http://llm-swap:11436"
	apiKey   string // optional; sent as "Authorization: Bearer <key>"
	httpLong *http.Client
}

var _ port.LLMInference = (*Client)(nil)

// New returns a client targeting baseURL (no trailing slash, no /v1 suffix —
// that's added per-request). Pass "" for apiKey when the server doesn't
// require auth.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL:  baseURL,
		apiKey:   apiKey,
		httpLong: &http.Client{Timeout: 600 * time.Second},
	}
}
