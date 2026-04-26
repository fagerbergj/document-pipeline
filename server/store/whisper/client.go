// Package whisper is an HTTP adapter for an OpenAI-compatible speech-to-text
// service such as faster-whisper-server. It implements port.Transcriber.
package whisper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// Client targets a single faster-whisper-server-style endpoint.
type Client struct {
	baseURL string
	http    *http.Client
}

var _ port.Transcriber = (*Client)(nil)

// New returns a Client. baseURL points at the service root (no trailing slash);
// e.g. "http://faster-whisper:8000".
func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		// 30 minutes covers a multi-hour large-v3 transcription on a decent GPU.
		http: &http.Client{Timeout: 30 * time.Minute},
	}
}

// Transcribe POSTs the audio bytes as multipart/form-data to
// {baseURL}/v1/audio/transcriptions with form fields `file` and `model`,
// matching the OpenAI Audio API and faster-whisper-server.
func (c *Client) Transcribe(ctx context.Context, model string, audio []byte, filename string) (string, error) {
	if filename == "" {
		filename = "audio.webm"
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("whisper: create form file: %w", err)
	}
	if _, err := io.Copy(fw, bytes.NewReader(audio)); err != nil {
		return "", fmt.Errorf("whisper: copy audio: %w", err)
	}
	if err := mw.WriteField("model", model); err != nil {
		return "", fmt.Errorf("whisper: write model field: %w", err)
	}
	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("whisper: close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/audio/transcriptions", &buf)
	if err != nil {
		return "", fmt.Errorf("whisper: build request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("whisper: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("whisper: HTTP %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("whisper: decode: %w", err)
	}
	return out.Text, nil
}
