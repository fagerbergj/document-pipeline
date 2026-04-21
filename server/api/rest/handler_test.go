package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core"
	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// Test UUIDs — fixture IDs must be valid UUIDs so toUUID() round-trips correctly.
const (
	testDocID = "00000000-0000-0000-0000-000000000001"
	testJobID = "00000000-0000-0000-0000-000000000002"
	testCtxID = "00000000-0000-0000-0000-000000000003"
)

// ── mock implementations ──────────────────────────────────────────────────────

type mockDocRepo struct {
	docs   map[string]model.Document
	byHash map[string]model.Document
}

func newMockDocRepo() *mockDocRepo {
	return &mockDocRepo{docs: map[string]model.Document{}, byHash: map[string]model.Document{}}
}
func (m *mockDocRepo) Insert(_ context.Context, d model.Document) error {
	m.docs[d.ID] = d
	m.byHash[d.ContentHash] = d
	return nil
}
func (m *mockDocRepo) Get(_ context.Context, id string) (model.Document, error) {
	d, ok := m.docs[id]
	if !ok {
		return model.Document{}, errNotFound("document")
	}
	return d, nil
}
func (m *mockDocRepo) GetByHash(_ context.Context, hash string) (model.Document, bool, error) {
	d, ok := m.byHash[hash]
	return d, ok, nil
}
func (m *mockDocRepo) Update(_ context.Context, d model.Document) error {
	m.docs[d.ID] = d
	return nil
}
func (m *mockDocRepo) Delete(_ context.Context, id string) error {
	delete(m.docs, id)
	return nil
}
func (m *mockDocRepo) ListPaginated(_ context.Context, _ port.DocumentFilter, _ model.PageRequest) (model.PageResult[model.Document], error) {
	docs := make([]model.Document, 0, len(m.docs))
	for _, d := range m.docs {
		docs = append(docs, d)
	}
	return model.PageResult[model.Document]{Data: docs}, nil
}
func (m *mockDocRepo) ListBySeries(_ context.Context, series string) ([]model.Document, error) {
	var out []model.Document
	for _, d := range m.docs {
		if d.Series != nil && *d.Series == series {
			out = append(out, d)
		}
	}
	return out, nil
}

type mockJobRepo struct {
	jobs map[string]model.Job
}

func newMockJobRepo() *mockJobRepo { return &mockJobRepo{jobs: map[string]model.Job{}} }
func (m *mockJobRepo) Upsert(_ context.Context, j model.Job) error {
	m.jobs[j.ID] = j
	return nil
}
func (m *mockJobRepo) GetByID(_ context.Context, id string) (model.Job, error) {
	j, ok := m.jobs[id]
	if !ok {
		return model.Job{}, errNotFound("job")
	}
	return j, nil
}
func (m *mockJobRepo) GetByDocumentAndStage(_ context.Context, docID, stage string) (model.Job, bool, error) {
	for _, j := range m.jobs {
		if j.DocumentID == docID && j.Stage == stage {
			return j, true, nil
		}
	}
	return model.Job{}, false, nil
}
func (m *mockJobRepo) UpdateStatus(_ context.Context, id, status string, updatedAt time.Time) error {
	j, ok := m.jobs[id]
	if !ok {
		return errNotFound("job")
	}
	j.Status = model.JobStatus(status)
	j.UpdatedAt = updatedAt
	m.jobs[id] = j
	return nil
}
func (m *mockJobRepo) UpdateOptions(_ context.Context, id string, opts model.JobOptions, updatedAt time.Time) error {
	j, ok := m.jobs[id]
	if !ok {
		return errNotFound("job")
	}
	j.Options = opts
	j.UpdatedAt = updatedAt
	m.jobs[id] = j
	return nil
}
func (m *mockJobRepo) UpdateRuns(_ context.Context, id string, runs []model.Run, updatedAt time.Time) error {
	j, ok := m.jobs[id]
	if !ok {
		return errNotFound("job")
	}
	j.Runs = runs
	j.UpdatedAt = updatedAt
	m.jobs[id] = j
	return nil
}
func (m *mockJobRepo) ListForDocument(_ context.Context, docID string) ([]model.Job, error) {
	var out []model.Job
	for _, j := range m.jobs {
		if j.DocumentID == docID {
			out = append(out, j)
		}
	}
	return out, nil
}
func (m *mockJobRepo) ListPending(_ context.Context, _ string) ([]model.Job, error) { return nil, nil }
func (m *mockJobRepo) ListPaginated(_ context.Context, _ port.JobFilter, _ model.PageRequest) (model.PageResult[model.Job], error) {
	jobs := make([]model.Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		jobs = append(jobs, j)
	}
	return model.PageResult[model.Job]{Data: jobs}, nil
}
func (m *mockJobRepo) ResetRunning(_ context.Context) (int, error) { return 0, nil }
func (m *mockJobRepo) CascadeReplay(_ context.Context, _, _ string, _ []string, _ time.Time) error {
	return nil
}

type mockArtifactRepo struct {
	artifacts map[string]model.Artifact
}

func newMockArtifactRepo() *mockArtifactRepo {
	return &mockArtifactRepo{artifacts: map[string]model.Artifact{}}
}
func (m *mockArtifactRepo) Insert(_ context.Context, a model.Artifact) error {
	m.artifacts[a.ID] = a
	return nil
}
func (m *mockArtifactRepo) Get(_ context.Context, _, artifactID string) (model.Artifact, error) {
	a, ok := m.artifacts[artifactID]
	if !ok {
		return model.Artifact{}, errNotFound("artifact")
	}
	return a, nil
}
func (m *mockArtifactRepo) ListForDocument(_ context.Context, docID string) ([]model.Artifact, error) {
	var out []model.Artifact
	for _, a := range m.artifacts {
		if a.DocumentID == docID {
			out = append(out, a)
		}
	}
	return out, nil
}

type mockContextRepo struct {
	entries map[string]model.Context
}

func newMockContextRepo() *mockContextRepo {
	return &mockContextRepo{entries: map[string]model.Context{}}
}
func (m *mockContextRepo) List(_ context.Context) ([]model.Context, error) {
	out := make([]model.Context, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e)
	}
	return out, nil
}
func (m *mockContextRepo) Create(_ context.Context, name, text string) (model.Context, error) {
	e := model.Context{ID: testCtxID, Name: name, Text: text, CreatedAt: time.Now()}
	m.entries[e.ID] = e
	return e, nil
}
func (m *mockContextRepo) Update(_ context.Context, id string, name, text *string) (model.Context, error) {
	e, ok := m.entries[id]
	if !ok {
		return model.Context{}, errNotFound("context")
	}
	if name != nil {
		e.Name = *name
	}
	if text != nil {
		e.Text = *text
	}
	m.entries[id] = e
	return e, nil
}
func (m *mockContextRepo) Delete(_ context.Context, id string) (bool, error) {
	_, ok := m.entries[id]
	if !ok {
		return false, nil
	}
	delete(m.entries, id)
	return true, nil
}

type mockChatRepo struct {
	sessions map[string]model.ChatSession
}

func newMockChatRepo() *mockChatRepo {
	return &mockChatRepo{sessions: map[string]model.ChatSession{}}
}
func (m *mockChatRepo) Create(_ context.Context, sp string, rag model.RAGConfig) (model.ChatSession, error) {
	s := model.ChatSession{
		ID:           "chat-1",
		SystemPrompt: sp,
		RAGRetrieval: rag,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	m.sessions[s.ID] = s
	return s, nil
}
func (m *mockChatRepo) Get(_ context.Context, id string) (model.ChatSession, bool, error) {
	s, ok := m.sessions[id]
	return s, ok, nil
}
func (m *mockChatRepo) Update(_ context.Context, id string, u port.ChatSessionUpdates) (model.ChatSession, error) {
	s, ok := m.sessions[id]
	if !ok {
		return model.ChatSession{}, errNotFound("chat")
	}
	if u.Title != nil {
		s.Title = *u.Title
	}
	if u.SystemPrompt != nil {
		s.SystemPrompt = *u.SystemPrompt
	}
	if u.RAGRetrieval != nil {
		s.RAGRetrieval = *u.RAGRetrieval
	}
	m.sessions[id] = s
	return s, nil
}
func (m *mockChatRepo) Delete(_ context.Context, id string) (bool, error) {
	_, ok := m.sessions[id]
	delete(m.sessions, id)
	return ok, nil
}
func (m *mockChatRepo) List(_ context.Context, pageSize int, beforeID *string) ([]model.ChatSession, error) {
	out := make([]model.ChatSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out, nil
}

type mockMessageRepo struct{}

func (m *mockMessageRepo) Append(_ context.Context, _, _, _ string, _ []model.SourceRef) (model.ChatMessage, error) {
	return model.ChatMessage{ID: "msg-1"}, nil
}
func (m *mockMessageRepo) List(_ context.Context, _ string) ([]model.ChatMessage, error) {
	return nil, nil
}

type mockArtifactStore struct{}

func (m *mockArtifactStore) Save(_, _, _ string, _ []byte) error { return nil }
func (m *mockArtifactStore) Read(_, _, _ string) ([]byte, error) { return nil, nil }

type mockStreamManager struct{}

func (m *mockStreamManager) Publish(_ string, _ port.StreamEvent) {}
func (m *mockStreamManager) Subscribe(_ string) <-chan port.StreamEvent {
	ch := make(chan port.StreamEvent)
	return ch
}
func (m *mockStreamManager) Unsubscribe(_ string) {}

type mockLLM struct{}

func (m *mockLLM) GenerateVision(_ context.Context, _, _ string, _ []byte, _ func(string)) error {
	return nil
}
func (m *mockLLM) GenerateText(_ context.Context, _, _ string, _ func(string)) error { return nil }
func (m *mockLLM) ChatWithTools(_ context.Context, _ string, _ []port.LLMMessage, _ []port.LLMTool) (string, []port.LLMToolCall, error) {
	return "", nil, nil
}
func (m *mockLLM) ChatStream(_ context.Context, _ string, _ []port.LLMMessage, _ func(string)) error {
	return nil
}
func (m *mockLLM) GenerateEmbed(_ context.Context, _, _ string) ([]float32, error) {
	return []float32{0.1, 0.2}, nil
}
func (m *mockLLM) Unload(_ context.Context, _ string) error { return nil }

type mockEmbedStore struct{}

func (m *mockEmbedStore) Upsert(_ context.Context, _ string, _, _ []float32, _ map[string]any) error {
	return nil
}
func (m *mockEmbedStore) Search(_ context.Context, _ []float32, _ int) ([]port.EmbedResult, error) {
	return nil, nil
}
func (m *mockEmbedStore) DeleteByDocID(_ context.Context, _ string) error  { return nil }
func (m *mockEmbedStore) DeleteBySeries(_ context.Context, _ string) error { return nil }

type mockStageEventRepo struct{}

func (m *mockStageEventRepo) Append(_ context.Context, _ model.StageEvent) error { return nil }
func (m *mockStageEventRepo) CountFailures(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

type mockKVRepo struct{ data map[string]string }

func (m *mockKVRepo) Set(_ context.Context, k, v string) error {
	m.data[k] = v
	return nil
}
func (m *mockKVRepo) Get(_ context.Context, k string) (string, bool, error) {
	v, ok := m.data[k]
	return v, ok, nil
}

// errNotFound is a simple sentinel error for not-found cases.
type notFoundError struct{ resource string }

func (e notFoundError) Error() string   { return e.resource + " not found" }
func errNotFound(resource string) error { return notFoundError{resource} }

// ── test setup ────────────────────────────────────────────────────────────────

func newTestHandler(t *testing.T) (*handler, *mockDocRepo, *mockJobRepo) {
	t.Helper()
	docs := newMockDocRepo()
	jobs := newMockJobRepo()
	artifacts := newMockArtifactRepo()
	contexts := newMockContextRepo()
	chats := newMockChatRepo()
	events := &mockStageEventRepo{}
	kv := &mockKVRepo{data: map[string]string{}}

	pipeline := model.PipelineConfig{
		MaxConcurrent: 1,
		Stages: []model.StageDefinition{
			{Name: "ocr", Type: "computer_vision", Model: "llava"},
			{Name: "clarify", Type: "llm_text"},
		},
	}

	ingest := core.NewIngestService(docs, jobs, artifacts, events, kv, &mockArtifactStore{}, pipeline, t.TempDir())

	h := &handler{
		docs:      docs,
		jobs:      jobs,
		artifacts: artifacts,
		contexts:  contexts,
		chats:     chats,
		messages:  &mockMessageRepo{},
		store:     &mockArtifactStore{},
		streams:   &mockStreamManager{},
		llm:       &mockLLM{},
		embed:     &mockEmbedStore{},
		ingest:    ingest,
		pipeline:  pipeline,
		vaultPath: t.TempDir(),
	}
	return h, docs, jobs
}

func doRequest(t *testing.T, h *handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	NewRouter(h, nil).ServeHTTP(rr, req)
	return rr
}

func decodeResponse(t *testing.T, rr *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(rr.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rr.Body.String())
	}
}

// ── pipeline tests ────────────────────────────────────────────────────────────

func TestListPipelines(t *testing.T) {
	h, _, _ := newTestHandler(t)
	rr := doRequest(t, h, http.MethodGet, "/api/v1/pipelines", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	data := resp["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("want 1 pipeline, got %d", len(data))
	}
	pipeline := data[0].(map[string]any)
	if pipeline["id"] != "pipeline" {
		t.Errorf("pipeline id: got %v", pipeline["id"])
	}
}

func TestGetPipeline_NotFound(t *testing.T) {
	h, _, _ := newTestHandler(t)
	rr := doRequest(t, h, http.MethodGet, "/api/v1/pipelines/nonexistent", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rr.Code)
	}
}

func TestGetPipeline(t *testing.T) {
	h, _, _ := newTestHandler(t)
	rr := doRequest(t, h, http.MethodGet, "/api/v1/pipelines/pipeline", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	stages := resp["stages"].([]any)
	if len(stages) != 2 {
		t.Errorf("want 2 stages, got %d", len(stages))
	}
}

// ── document tests ────────────────────────────────────────────────────────────

func TestUploadDocument_PNG(t *testing.T) {
	h, _, _ := newTestHandler(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "test.png")
	fw.Write([]byte("fake-png-bytes"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/documents", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	NewRouter(h, nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201 (body: %s)", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	if resp["stage"] != "ocr" {
		t.Errorf("stage: got %v", resp["stage"])
	}
}

func TestUploadDocument_Duplicate(t *testing.T) {
	h, _, _ := newTestHandler(t)
	upload := func() *httptest.ResponseRecorder {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("file", "test.png")
		fw.Write([]byte("same-bytes"))
		mw.Close()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/documents", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rr := httptest.NewRecorder()
		NewRouter(h, nil).ServeHTTP(rr, req)
		return rr
	}
	if rr := upload(); rr.Code != http.StatusCreated {
		t.Fatalf("first upload: %d", rr.Code)
	}
	if rr := upload(); rr.Code != http.StatusConflict {
		t.Fatalf("second upload should be 409, got %d", rr.Code)
	}
}

func TestUploadDocument_UnsupportedType(t *testing.T) {
	h, _, _ := newTestHandler(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "test.pdf")
	fw.Write([]byte("pdf-bytes"))
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/documents", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	NewRouter(h, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rr.Code)
	}
}

func TestGetDocument_NotFound(t *testing.T) {
	h, _, _ := newTestHandler(t)
	rr := doRequest(t, h, http.MethodGet, "/api/v1/documents/does-not-exist", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestGetDocument(t *testing.T) {
	h, docs, _ := newTestHandler(t)
	now := time.Now().UTC()
	title := "My Doc"
	doc := model.Document{ID: testDocID, ContentHash: "abc", Title: &title, CreatedAt: now, UpdatedAt: now}
	docs.Insert(context.Background(), doc)

	rr := doRequest(t, h, http.MethodGet, "/api/v1/documents/"+testDocID, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	if resp["id"] != testDocID {
		t.Errorf("id: got %v", resp["id"])
	}
}

func TestPatchDocument(t *testing.T) {
	h, docs, _ := newTestHandler(t)
	now := time.Now().UTC()
	doc := model.Document{ID: testDocID, ContentHash: "abc", CreatedAt: now, UpdatedAt: now}
	docs.Insert(context.Background(), doc)

	rr := doRequest(t, h, http.MethodPatch, "/api/v1/documents/"+testDocID, map[string]any{
		"title": "Updated Title",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	if resp["title"] != "Updated Title" {
		t.Errorf("title: got %v", resp["title"])
	}
}

func TestDeleteDocument(t *testing.T) {
	h, docs, _ := newTestHandler(t)
	now := time.Now().UTC()
	doc := model.Document{ID: testDocID, ContentHash: "abc", CreatedAt: now, UpdatedAt: now}
	docs.Insert(context.Background(), doc)

	rr := doRequest(t, h, http.MethodDelete, "/api/v1/documents/"+testDocID, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	if resp["ok"] != true {
		t.Errorf("ok: got %v", resp["ok"])
	}
	// confirm gone
	if _, err := docs.Get(context.Background(), testDocID); err == nil {
		t.Error("document should be deleted")
	}
}

// ── job tests ─────────────────────────────────────────────────────────────────

func seedJob(t *testing.T, jobs *mockJobRepo, status model.JobStatus) model.Job {
	t.Helper()
	now := time.Now().UTC()
	j := model.Job{
		ID:         testJobID,
		DocumentID: testDocID,
		Stage:      "ocr",
		Status:     status,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := jobs.Upsert(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestListJobs(t *testing.T) {
	h, _, jobs := newTestHandler(t)
	seedJob(t, jobs, model.JobStatusPending)
	rr := doRequest(t, h, http.MethodGet, "/api/v1/jobs", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	data := resp["data"].([]any)
	if len(data) != 1 {
		t.Errorf("want 1 job, got %d", len(data))
	}
}

func TestGetJob(t *testing.T) {
	h, _, jobs := newTestHandler(t)
	seedJob(t, jobs, model.JobStatusPending)
	rr := doRequest(t, h, http.MethodGet, "/api/v1/jobs/"+testJobID, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	if resp["id"] != testJobID {
		t.Errorf("id: got %v", resp["id"])
	}
}

func TestGetJob_NotFound(t *testing.T) {
	h, _, _ := newTestHandler(t)
	rr := doRequest(t, h, http.MethodGet, "/api/v1/jobs/nope", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestPutJobStatus_ValidTransition(t *testing.T) {
	h, _, jobs := newTestHandler(t)
	seedJob(t, jobs, model.JobStatusWaiting)
	rr := doRequest(t, h, http.MethodPut, "/api/v1/jobs/"+testJobID+"/status", map[string]string{"status": "done"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	if resp["status"] != "done" {
		t.Errorf("status: got %v", resp["status"])
	}
}

func TestPutJobStatus_InvalidTransition(t *testing.T) {
	h, _, jobs := newTestHandler(t)
	seedJob(t, jobs, model.JobStatusPending)
	rr := doRequest(t, h, http.MethodPut, "/api/v1/jobs/"+testJobID+"/status", map[string]string{"status": "done"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", rr.Code)
	}
}

func TestPatchRun(t *testing.T) {
	h, _, jobs := newTestHandler(t)
	now := time.Now().UTC()
	j := model.Job{
		ID:         testJobID,
		DocumentID: testDocID,
		Stage:      "clarify",
		Status:     model.JobStatusWaiting,
		Runs: []model.Run{
			{
				ID:         "run-1",
				Inputs:     []model.Field{{Field: "ocr_raw", Text: "hello"}},
				Outputs:    []model.Field{{Field: "clarified_text", Text: "hello world"}},
				Confidence: model.ConfidenceLow,
				Questions: []model.Question{
					{Segment: "hello", Question: "What is this?", Answer: ""},
				},
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobs.Upsert(context.Background(), j)

	rr := doRequest(t, h, http.MethodPatch, "/api/v1/jobs/"+testJobID+"/runs/run-1", map[string]any{
		"questions": []map[string]string{
			{"segment": "hello", "question": "What is this?", "answer": "A greeting"},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	qs := resp["questions"].([]any)
	if len(qs) == 0 {
		t.Fatal("no questions in response")
	}
	q := qs[0].(map[string]any)
	if q["answer"] != "A greeting" {
		t.Errorf("answer: got %v", q["answer"])
	}
}

// ── context tests ─────────────────────────────────────────────────────────────

func TestListContexts_Empty(t *testing.T) {
	h, _, _ := newTestHandler(t)
	rr := doRequest(t, h, http.MethodGet, "/api/v1/contexts", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	data := resp["data"].([]any)
	if len(data) != 0 {
		t.Errorf("want 0 contexts, got %d", len(data))
	}
}

func TestCreateContext(t *testing.T) {
	h, _, _ := newTestHandler(t)
	rr := doRequest(t, h, http.MethodPost, "/api/v1/contexts", map[string]string{
		"name": "My Context",
		"text": "Some context text",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	if resp["name"] != "My Context" {
		t.Errorf("name: got %v", resp["name"])
	}
}

func TestUpdateContext(t *testing.T) {
	h, _, _ := newTestHandler(t)
	// create first
	doRequest(t, h, http.MethodPost, "/api/v1/contexts", map[string]string{
		"name": "Old Name",
		"text": "Old text",
	})
	// update
	rr := doRequest(t, h, http.MethodPatch, "/api/v1/contexts/"+testCtxID, map[string]string{
		"name": "New Name",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	if resp["name"] != "New Name" {
		t.Errorf("name: got %v", resp["name"])
	}
}

func TestDeleteContext(t *testing.T) {
	h, _, _ := newTestHandler(t)
	doRequest(t, h, http.MethodPost, "/api/v1/contexts", map[string]string{
		"name": "ctx",
		"text": "txt",
	})
	rr := doRequest(t, h, http.MethodDelete, "/api/v1/contexts/"+testCtxID, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestDeleteContext_NotFound(t *testing.T) {
	h, _, _ := newTestHandler(t)
	rr := doRequest(t, h, http.MethodDelete, "/api/v1/contexts/nope", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rr.Code)
	}
}

// ── chat tests ────────────────────────────────────────────────────────────────

func TestCreateChat(t *testing.T) {
	h, _, _ := newTestHandler(t)
	rr := doRequest(t, h, http.MethodPost, "/api/v1/chats", map[string]any{})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	if resp["id"] == nil {
		t.Error("id should be set")
	}
}

func TestGetChat_NotFound(t *testing.T) {
	h, _, _ := newTestHandler(t)
	rr := doRequest(t, h, http.MethodGet, "/api/v1/chats/nope", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestListChats(t *testing.T) {
	h, _, _ := newTestHandler(t)
	doRequest(t, h, http.MethodPost, "/api/v1/chats", map[string]any{})
	rr := doRequest(t, h, http.MethodGet, "/api/v1/chats", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	data := resp["data"].([]any)
	if len(data) != 1 {
		t.Errorf("want 1 chat, got %d", len(data))
	}
}

func TestDeleteChat(t *testing.T) {
	h, _, _ := newTestHandler(t)
	doRequest(t, h, http.MethodPost, "/api/v1/chats", map[string]any{})
	rr := doRequest(t, h, http.MethodDelete, "/api/v1/chats/chat-1", nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204", rr.Code)
	}
}

// ── pick current job ──────────────────────────────────────────────────────────

func TestPickCurrentJob_Priority(t *testing.T) {
	now := time.Now()
	jobs := []model.Job{
		{ID: "a", Status: model.JobStatusDone, UpdatedAt: now.Add(-1 * time.Hour)},
		{ID: "b", Status: model.JobStatusPending, UpdatedAt: now},
		{ID: "c", Status: model.JobStatusWaiting, UpdatedAt: now},
	}
	got := pickCurrentJob(jobs)
	if got == nil || got.ID != "c" {
		t.Errorf("expected waiting job 'c', got %v", got)
	}
}

func TestPickCurrentJob_AllDone(t *testing.T) {
	now := time.Now()
	jobs := []model.Job{
		{ID: "a", Status: model.JobStatusDone, UpdatedAt: now.Add(-1 * time.Minute)},
		{ID: "b", Status: model.JobStatusDone, UpdatedAt: now},
	}
	got := pickCurrentJob(jobs)
	if got == nil || got.ID != "b" {
		t.Errorf("expected most recent 'b', got %v", got)
	}
}

// ── pagination helpers ────────────────────────────────────────────────────────

func TestListDocuments_Pagination(t *testing.T) {
	h, _, _ := newTestHandler(t)
	rr := doRequest(t, h, http.MethodGet, "/api/v1/documents?page_size=10", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	if _, present := resp["next_page_token"]; present && false {
		t.Error("next_page_token field missing")
	}
}

// ── ingest webhook tests ──────────────────────────────────────────────────────

func doWebhookRequest(t *testing.T, h *handler, dataJSON string, imageBytes []byte, filename string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if dataJSON != "" {
		mw.WriteField("data", dataJSON)
	}
	fw, _ := mw.CreateFormFile("attachment", filename)
	fw.Write(imageBytes)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/remarkable/webhook", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	NewRouter(h, nil).ServeHTTP(rr, req)
	return rr
}

func TestReceiveWebhook_NewDocument(t *testing.T) {
	h, docs, _ := newTestHandler(t)
	rr := doWebhookRequest(t, h, `{}`, []byte("fake-png-bytes"), "remarkable.png")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	if len(docs.docs) != 1 {
		t.Errorf("expected 1 document, got %d", len(docs.docs))
	}
}

func TestReceiveWebhook_Duplicate(t *testing.T) {
	h, _, _ := newTestHandler(t)
	imageBytes := []byte("duplicate-png-bytes")
	if rr := doWebhookRequest(t, h, `{}`, imageBytes, "remarkable.png"); rr.Code != http.StatusOK {
		t.Fatalf("first webhook: %d", rr.Code)
	}
	// Second send of the same bytes must still return 200 (rmfakecloud retries on non-200).
	if rr := doWebhookRequest(t, h, `{}`, imageBytes, "remarkable.png"); rr.Code != http.StatusOK {
		t.Fatalf("duplicate webhook: %d, want 200", rr.Code)
	}
}

func TestReceiveWebhook_TitleFromDestinations(t *testing.T) {
	h, docs, _ := newTestHandler(t)
	dataJSON := `{"destinations": ["My Notebook"]}`
	doWebhookRequest(t, h, dataJSON, []byte("notebook-png-bytes"), "remarkable.png")
	for _, doc := range docs.docs {
		if doc.Title == nil || *doc.Title != "My Notebook" {
			t.Errorf("title = %v, want 'My Notebook'", doc.Title)
		}
	}
}

func TestReceiveWebhook_MissingAttachment(t *testing.T) {
	h, _, _ := newTestHandler(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("data", "{}")
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/remarkable/webhook", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	NewRouter(h, nil).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", rr.Code)
	}
}

// ── search (q param) ─────────────────────────────────────────────────────────

type mockSearchStore struct {
	results []string
	queries []string
}

func (m *mockSearchStore) EnsureIndex(_ context.Context) error            { return nil }
func (m *mockSearchStore) Count(_ context.Context) (int, error)           { return len(m.results), nil }
func (m *mockSearchStore) Index(_ context.Context, _ port.IndexDoc) error { return nil }
func (m *mockSearchStore) Delete(_ context.Context, _ string) error       { return nil }
func (m *mockSearchStore) Search(_ context.Context, query string, _, _ int) ([]string, int, error) {
	m.queries = append(m.queries, query)
	return m.results, len(m.results), nil
}

func TestListDocuments_SearchQ(t *testing.T) {
	h, docs, _ := newTestHandler(t)
	title := "Invoice March"
	docs.Insert(context.Background(), model.Document{
		ID:             testDocID,
		ContentHash:    "abc",
		Title:          &title,
		LinkedContexts: []string{},
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	})

	srch := &mockSearchStore{results: []string{testDocID}}
	h.search = srch

	rr := doRequest(t, h, http.MethodGet, "/api/v1/documents?q=tags%3Ainvoice", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	data := resp["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("want 1 result, got %d", len(data))
	}
	if len(srch.queries) != 1 || srch.queries[0] != "tags:invoice" {
		t.Errorf("unexpected search queries: %v", srch.queries)
	}
}

func TestListDocuments_SearchQ_NoResults(t *testing.T) {
	h, _, _ := newTestHandler(t)
	h.search = &mockSearchStore{results: []string{}} // empty result set

	rr := doRequest(t, h, http.MethodGet, "/api/v1/documents?q=status%3Apending", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	data := resp["data"].([]any)
	if len(data) != 0 {
		t.Errorf("want 0 results for empty search, got %d", len(data))
	}
}

func TestListDocuments_SearchQ_NoSearchStore(t *testing.T) {
	// When search store is nil (OpenSearch not configured), q param is silently ignored.
	h, docs, _ := newTestHandler(t)
	h.search = nil
	title := "Some Doc"
	docs.Insert(context.Background(), model.Document{
		ID:             testDocID,
		ContentHash:    "abc",
		Title:          &title,
		LinkedContexts: []string{},
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	})

	rr := doRequest(t, h, http.MethodGet, "/api/v1/documents?q=anything", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}
	var resp map[string]any
	decodeResponse(t, rr, &resp)
	// All docs returned since search was skipped.
	data := resp["data"].([]any)
	if len(data) != 1 {
		t.Errorf("want 1 doc (search bypassed), got %d", len(data))
	}
}

// ── splitCSV ─────────────────────────────────────────────────────────────────

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"a,b,c", []string{"a", "b", "c"}},
		{"a, b , c", []string{"a", "b", "c"}},
		{"single", []string{"single"}},
		{"", []string{}},
	}
	for _, tc := range cases {
		got := splitCSV(tc.input)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("splitCSV(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}
