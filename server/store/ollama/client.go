package ollama

import (
	"net/http"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// Client implements port.LLMInference against the Ollama HTTP API.
type Client struct {
	baseURL   string
	httpLong  *http.Client // 600s — generation / vision
	httpEmbed *http.Client // 300s — embeddings
}

var _ port.LLMInference = (*Client)(nil)

func New(baseURL string) *Client {
	return &Client{
		baseURL:   baseURL,
		httpLong:  &http.Client{Timeout: 600 * time.Second},
		httpEmbed: &http.Client{Timeout: 300 * time.Second},
	}
}
