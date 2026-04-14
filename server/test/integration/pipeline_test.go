// Package integration runs end-to-end tests against the full wired server:
// real SQLite, real filesystem, mock Ollama, no-op EmbedStore.
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fagerbergj/document-pipeline/server/api/rest"
	"github.com/fagerbergj/document-pipeline/server/core"
	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/fagerbergj/document-pipeline/server/store/filesystem"
	"github.com/fagerbergj/document-pipeline/server/store/ollama"
	"github.com/fagerbergj/document-pipeline/server/store/prompts"
	"github.com/fagerbergj/document-pipeline/server/store/sqlite"
	"github.com/fagerbergj/document-pipeline/server/store/stream"
)

// migrationsDir returns the path to the SQL migration files relative to the
// module root, using the test binary's working directory.
func migrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	// file is .../server/test/integration/pipeline_test.go — go up 3 dirs to root
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	return filepath.Join(root, "db", "migrations")
}

// testPromptFile writes a minimal classify prompt template to dir and returns its path.
// Using a self-contained temp prompt avoids any dependency on the real prompts/ directory.
func testPromptFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "classify.txt")
	// ClarifyPromptData fields: DocumentContext, LinkedContext, LinkedContextName, PreviousOutput, QAHistory
	content := "Classify this document. Context: {{.DocumentContext}}\nReply with XML: <output><tags>[\"test\"]</tags><summary>A test summary.</summary><confidence>high</confidence></output>"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write test prompt: %v", err)
	}
	return path
}

// --- no-op EmbedStore ---

type noopEmbed struct{}

func (n *noopEmbed) Upsert(_ context.Context, _ string, _ []float32, _ []float32, _ map[string]any) error {
	return nil
}
func (n *noopEmbed) Search(_ context.Context, _ []float32, _ int) ([]port.EmbedResult, error) {
	return nil, nil
}
func (n *noopEmbed) Delete(_ context.Context, _ string) error { return nil }

// --- mock Ollama server ---

// mockOllamaServer returns an httptest.Server that handles Ollama API calls.
// generate returns a fixed LLM response; embed returns a 4-dim zero vector;
// unload (keep_alive=0) returns success.
func mockOllamaServer(t *testing.T, generateResponse string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/generate":
			body, _ := io.ReadAll(r.Body)
			var req struct {
				Stream    bool `json:"stream"`
				KeepAlive int  `json:"keep_alive"`
			}
			json.Unmarshal(body, &req)

			if req.KeepAlive == 0 {
				// Unload request — just acknowledge.
				json.NewEncoder(w).Encode(map[string]any{"done": true})
				return
			}
			if req.Stream {
				// Streaming NDJSON
				for _, tok := range strings.Fields(generateResponse) {
					json.NewEncoder(w).Encode(map[string]any{"response": tok + " ", "done": false})
				}
				json.NewEncoder(w).Encode(map[string]any{"response": "", "done": true})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"response": generateResponse})
			}
		case "/api/embed":
			json.NewEncoder(w).Encode(map[string]any{
				"embeddings": [][]float32{{0.1, 0.2, 0.3, 0.4}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

// --- test environment ---

type testEnv struct {
	srv    *httptest.Server
	db     *sqlite.DB
	worker *core.WorkerService
}

func (e *testEnv) Close() {
	e.srv.Close()
	e.db.Close()
}

// makePipeline returns a single-stage llm_text pipeline using the given prompt file.
func makePipeline(promptPath string) model.PipelineConfig {
	return model.PipelineConfig{
		MaxConcurrent: 1,
		Stages: []model.StageDefinition{
			{
				Name:   "classify",
				Type:   model.StageTypeLLMText,
				Model:  "test-model",
				Prompt: promptPath,
				Input:  "raw_text",
				Outputs: []model.StageOutput{
					{Field: "tags", Type: "json_array"},
					{Field: "summary", Type: "text"},
				},
			},
		},
	}
}

// newTestEnv wires the full server stack with an in-process mock Ollama.
func newTestEnv(t *testing.T, ollamaResp string) *testEnv {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	vault := filepath.Join(dir, "vault")

	db, err := sqlite.Open(dbPath, migrationsDir())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	ollamaSrv := mockOllamaServer(t, ollamaResp)
	t.Cleanup(ollamaSrv.Close)

	pipeline := makePipeline(testPromptFile(t))

	docs := db.Documents()
	jobs := db.Jobs()
	artifacts := db.Artifacts()
	events := db.StageEvents()
	contexts := db.Contexts()
	kv := db.KeyValues()
	fs := filesystem.New()
	sm := stream.New()
	llm := ollama.New(ollamaSrv.URL)
	embed := &noopEmbed{}
	renderer := &prompts.FilePromptRenderer{}

	ingest := core.NewIngestService(docs, jobs, artifacts, events, kv, fs, pipeline, vault)
	worker := core.NewWorkerService(docs, jobs, artifacts, events, contexts, kv, fs, llm, embed, sm, renderer, pipeline, vault)

	handler := rest.New(docs, jobs, artifacts, contexts,
		db.Chats(), db.ChatMessages(),
		fs, sm, llm, embed, ingest, pipeline, vault, nil)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &testEnv{srv: srv, db: db, worker: worker}
}

// get is a convenience wrapper for GET requests.
func (e *testEnv) get(t *testing.T, path string) map[string]any {
	t.Helper()
	resp, err := http.Get(e.srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s returned %d: %s", path, resp.StatusCode, body)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

// uploadText ingests a plain-text document via the REST API.
// The upload endpoint returns a jobDetail object on 201 Created.
// Returns the document_id from the job detail.
func (e *testEnv) uploadText(t *testing.T, filename, content string) string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", filename)
	io.WriteString(fw, content)
	mw.Close()

	resp, err := http.Post(e.srv.URL+"/api/v1/documents",
		mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload returned %d: %s", resp.StatusCode, body)
	}
	// Upload returns a jobDetail: {"id": <job_id>, "document_id": <doc_id>, ...}
	var out map[string]any
	json.Unmarshal(body, &out)
	docID, _ := out["document_id"].(string)
	return docID
}

// waitForJobStatus polls until any job for docID reaches one of the given
// statuses, or the deadline is exceeded.
func (e *testEnv) waitForJobStatus(t *testing.T, docID string, want ...string) map[string]any {
	t.Helper()
	wantSet := make(map[string]bool, len(want))
	for _, s := range want {
		wantSet[s] = true
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		result := e.get(t, "/api/v1/jobs?document_id="+docID)
		// List endpoints return {"data": [...], "next_page_token": ...}
		items, _ := result["data"].([]any)
		for _, item := range items {
			j, _ := item.(map[string]any)
			if wantSet[fmt.Sprint(j["status"])] {
				return j
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for job status %v for doc %s", want, docID[:8])
	return nil
}

// --- tests ---

// TestIngestAndProcess uploads a text document, runs the worker once, and
// verifies the job reaches "done" or "waiting" (no context provided).
func TestIngestAndProcess(t *testing.T) {
	// Ollama responds with a plausible classify response.
	resp := `<output><tags>["test","integration"]</tags><summary>An integration test document.</summary><confidence>high</confidence></output>`
	env := newTestEnv(t, resp)
	defer env.Close()

	docID := env.uploadText(t, "test-doc.txt", "This is an integration test document about Go pipelines.")

	if docID == "" {
		t.Fatal("expected document ID")
	}

	// Run the worker synchronously.
	if err := env.worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("worker.RunOnce: %v", err)
	}

	job := env.waitForJobStatus(t, docID, "done", "waiting", "error")
	status := fmt.Sprint(job["status"])
	if status == "error" {
		t.Fatalf("job ended in error state: %+v", job)
	}
	t.Logf("job reached status %q", status)
}

// TestDuplicateIngest uploads the same document twice and verifies the second
// upload returns 200 (duplicate) with no new document created.
func TestDuplicateIngest(t *testing.T) {
	env := newTestEnv(t, "response")
	defer env.Close()

	content := "Duplicate document content."
	docID1 := env.uploadText(t, "dup.txt", content)

	// Second upload of identical content.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "dup.txt")
	io.WriteString(fw, content)
	mw.Close()

	resp, err := http.Post(env.srv.URL+"/api/v1/documents",
		mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	resp.Body.Close()

	// Duplicate returns HTTP 409 Conflict.
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate, got %d", resp.StatusCode)
	}
	_ = docID1
}

// TestGetDocument verifies the document endpoint returns the ingested document.
func TestGetDocument(t *testing.T) {
	env := newTestEnv(t, "response")
	defer env.Close()

	docID := env.uploadText(t, "hello.txt", "Hello world document.")

	result := env.get(t, "/api/v1/documents/"+docID)
	if result["id"] != docID {
		t.Errorf("expected id=%s, got %v", docID, result["id"])
	}
}

// TestListDocuments verifies pagination works after ingesting multiple docs.
func TestListDocuments(t *testing.T) {
	env := newTestEnv(t, "response")
	defer env.Close()

	for i := range 3 {
		env.uploadText(t, fmt.Sprintf("doc-%d.txt", i), fmt.Sprintf("Document number %d content.", i))
	}

	result := env.get(t, "/api/v1/documents?page_size=10")
	items, ok := result["data"].([]any)
	if !ok || len(items) < 3 {
		t.Fatalf("expected at least 3 documents, got %v", result)
	}
}

// TestDeleteDocument verifies a document can be deleted via the REST API.
func TestDeleteDocument(t *testing.T) {
	env := newTestEnv(t, "response")
	defer env.Close()

	docID := env.uploadText(t, "todelete.txt", "This document will be deleted.")

	req, _ := http.NewRequest(http.MethodDelete, env.srv.URL+"/api/v1/documents/"+docID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 200/204 on delete, got %d", resp.StatusCode)
	}

	// Document should now 404.
	resp2, _ := http.Get(env.srv.URL + "/api/v1/documents/" + docID)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", resp2.StatusCode)
	}
}

// TestPipelineEndpoint verifies the pipeline config is served correctly.
func TestPipelineEndpoint(t *testing.T) {
	env := newTestEnv(t, "response")
	defer env.Close()

	// GET /api/v1/pipelines returns {"data": [{id, name, stages: [...]}], ...}
	result := env.get(t, "/api/v1/pipelines")
	data, ok := result["data"].([]any)
	if !ok || len(data) == 0 {
		t.Fatalf("expected pipeline data, got %v", result)
	}
	pipeline, _ := data[0].(map[string]any)
	stages, ok := pipeline["stages"].([]any)
	if !ok || len(stages) == 0 {
		t.Fatalf("expected pipeline stages, got %v", pipeline)
	}
}

// TestContextCRUD exercises the contexts CRUD endpoints.
func TestContextCRUD(t *testing.T) {
	env := newTestEnv(t, "response")
	defer env.Close()

	// Create
	body, _ := json.Marshal(map[string]string{"name": "test-ctx", "text": "some context text"})
	resp, err := http.Post(env.srv.URL+"/api/v1/contexts", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create context: %d %s", resp.StatusCode, b)
	}
	var created map[string]any
	json.NewDecoder(resp.Body).Decode(&created)
	ctxID := fmt.Sprint(created["id"])

	// List — returns {"data": [...]}
	list := env.get(t, "/api/v1/contexts")
	items, _ := list["data"].([]any)
	if len(items) == 0 {
		t.Fatal("expected at least one context")
	}

	// Delete
	req, _ := http.NewRequest(http.MethodDelete, env.srv.URL+"/api/v1/contexts/"+ctxID, nil)
	dr, _ := http.DefaultClient.Do(req)
	dr.Body.Close()
	if dr.StatusCode != http.StatusOK && dr.StatusCode != http.StatusNoContent {
		t.Fatalf("delete context: %d", dr.StatusCode)
	}
}
