// Package openwebui is an HTTP client for the Open WebUI knowledge-base API.
// It is used by store/embed.EmbedStoreCoordinator and is not a port itself.
package openwebui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strings"
)

// Client uploads documents as markdown files to an Open WebUI knowledge base.
type Client struct {
	baseURL     string
	apiKey      string
	knowledgeID string
	http        *http.Client
}

func New(baseURL, apiKey, knowledgeID string) *Client {
	return &Client{
		baseURL:     baseURL,
		apiKey:      apiKey,
		knowledgeID: knowledgeID,
		http:        &http.Client{Timeout: 60 * 1e9},
	}
}

func (c *Client) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + c.apiKey}
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers() {
		req.Header.Set(k, v)
	}
	return c.http.Do(req)
}

func readBodyClose(r io.ReadCloser) string {
	b, _ := io.ReadAll(io.LimitReader(r, 512))
	r.Close()
	return string(b)
}

// buildMarkdown renders the document as markdown with YAML frontmatter.
func buildMarkdown(title, text string, metadata map[string]any) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("title: %q\n", title))
	for k, v := range metadata {
		switch val := v.(type) {
		case []any:
			b, _ := json.Marshal(val)
			sb.WriteString(fmt.Sprintf("%s: %s\n", k, b))
		case nil:
			// skip
		default:
			sb.WriteString(fmt.Sprintf("%s: %q\n", k, fmt.Sprint(val)))
		}
	}
	sb.WriteString("---\n\n# ")
	sb.WriteString(title)
	sb.WriteString("\n\n")
	sb.WriteString(text)
	return sb.String()
}

// Upsert uploads a document as markdown to Open WebUI and adds it to the knowledge base.
// If a file with the same doc ID already exists it is deleted first.
func (c *Client) Upsert(ctx context.Context, docID, title, text string, metadata map[string]any) error {
	filename := docID + ".md"
	content := buildMarkdown(title, text, metadata)

	// Delete any existing file for this doc.
	for _, name := range []string{filename, docID + ".txt"} {
		if id, ok := c.findFile(ctx, name); ok {
			c.deleteFile(ctx, id)
		}
	}

	// Upload the markdown file.
	fileID, err := c.uploadFile(ctx, filename, content)
	if err != nil {
		return err
	}
	slog.Info("uploaded file to Open WebUI", "filename", filename, "file_id", fileID)

	// Add to knowledge base.
	resp, err := c.doJSON(ctx, http.MethodPost, "/api/v1/knowledge/"+c.knowledgeID+"/file/add", map[string]string{"file_id": fileID})
	if err != nil {
		return err
	}
	body := readBodyClose(resp.Body)
	if resp.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(body), "duplicate") {
		slog.Warn("open webui duplicate content — file uploaded but not added to KB", "doc_id", docID[:8])
		return nil
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("openwebui: add to knowledge base %d: %s", resp.StatusCode, body)
	}
	slog.Info("added to knowledge base", "doc_id", docID[:8], "knowledge_id", c.knowledgeID)
	return nil
}

// Delete removes the document's file from the knowledge base and Open WebUI.
func (c *Client) Delete(ctx context.Context, docID string) error {
	for _, name := range []string{docID + ".md", docID + ".txt"} {
		if id, ok := c.findFile(ctx, name); ok {
			c.deleteFile(ctx, id)
		}
	}
	return nil
}

func (c *Client) uploadFile(ctx context.Context, filename, content string) (string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := io.WriteString(fw, content); err != nil {
		return "", err
	}
	if err := mw.WriteField("metadata", "{}"); err != nil {
		return "", err
	}
	mw.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/files/", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for k, v := range c.headers() {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	body := readBodyClose(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("openwebui: file upload %d: %s", resp.StatusCode, body)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return "", fmt.Errorf("openwebui: decode upload response: %w", err)
	}
	return out.ID, nil
}

func (c *Client) findFile(ctx context.Context, filename string) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/files/", nil)
	if err != nil {
		return "", false
	}
	for k, v := range c.headers() {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil || resp.StatusCode >= 300 {
		if resp != nil {
			resp.Body.Close()
		}
		return "", false
	}
	defer resp.Body.Close()
	var files []struct {
		ID       string `json:"id"`
		Filename string `json:"filename"`
	}
	// API may return a list directly or wrapped in {"files": [...]}
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &files); err != nil {
		var wrapped struct {
			Files []struct {
				ID       string `json:"id"`
				Filename string `json:"filename"`
			} `json:"files"`
		}
		if err2 := json.Unmarshal(raw, &wrapped); err2 == nil {
			files = wrapped.Files
		}
	}
	for _, f := range files {
		if f.Filename == filename {
			return f.ID, true
		}
	}
	return "", false
}

func (c *Client) deleteFile(ctx context.Context, fileID string) {
	// Remove from knowledge base first.
	resp, err := c.doJSON(ctx, http.MethodPost, "/api/v1/knowledge/"+c.knowledgeID+"/file/remove", map[string]string{"file_id": fileID})
	if err == nil {
		resp.Body.Close()
	}
	// Delete the file itself.
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/api/v1/files/"+fileID, nil)
	if err != nil {
		return
	}
	for k, v := range c.headers() {
		req.Header.Set(k, v)
	}
	del, err := c.http.Do(req)
	if err == nil {
		del.Body.Close()
		slog.Info("deleted file from Open WebUI", "file_id", fileID)
	}
}
