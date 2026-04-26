package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/adk/session"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/google/uuid"
)

// ---- mock implementations ----

type mockDocRepo struct {
	mu   sync.Mutex
	docs map[string]model.Document
}

func newMockDocRepo(docs ...model.Document) *mockDocRepo {
	m := &mockDocRepo{docs: map[string]model.Document{}}
	for _, d := range docs {
		m.docs[d.ID] = d
	}
	return m
}

func (m *mockDocRepo) Insert(ctx context.Context, doc model.Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs[doc.ID] = doc
	return nil
}
func (m *mockDocRepo) Get(ctx context.Context, id string) (model.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.docs[id]
	if !ok {
		return model.Document{}, fmt.Errorf("not found: %s", id)
	}
	return d, nil
}
func (m *mockDocRepo) GetByHash(ctx context.Context, hash string) (model.Document, bool, error) {
	return model.Document{}, false, nil
}
func (m *mockDocRepo) Update(ctx context.Context, doc model.Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs[doc.ID] = doc
	return nil
}
func (m *mockDocRepo) Delete(ctx context.Context, id string) error { return nil }
func (m *mockDocRepo) ListPaginated(ctx context.Context, filter port.DocumentFilter, page model.PageRequest) (model.PageResult[model.Document], error) {
	return model.PageResult[model.Document]{}, nil
}
func (m *mockDocRepo) ListBySeries(ctx context.Context, series string) ([]model.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []model.Document
	for _, d := range m.docs {
		if d.Series != nil && *d.Series == series {
			out = append(out, d)
		}
	}
	return out, nil
}

type mockJobRepo struct {
	mu       sync.Mutex
	jobs     map[string]model.Job
	statuses map[string]string
}

func newMockJobRepo(jobs ...model.Job) *mockJobRepo {
	m := &mockJobRepo{jobs: map[string]model.Job{}, statuses: map[string]string{}}
	for _, j := range jobs {
		m.jobs[j.ID] = j
		m.statuses[j.ID] = string(j.Status)
	}
	return m
}

func (m *mockJobRepo) Upsert(ctx context.Context, job model.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[job.ID] = job
	m.statuses[job.ID] = string(job.Status)
	return nil
}
func (m *mockJobRepo) GetByID(ctx context.Context, id string) (model.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return model.Job{}, fmt.Errorf("not found: %s", id)
	}
	return j, nil
}
func (m *mockJobRepo) GetByDocumentAndStage(ctx context.Context, documentID, stage string) (model.Job, bool, error) {
	return model.Job{}, false, nil
}
func (m *mockJobRepo) UpdateStatus(ctx context.Context, id, status string, updatedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses[id] = status
	if j, ok := m.jobs[id]; ok {
		j.Status = model.JobStatus(status)
		m.jobs[id] = j
	}
	return nil
}
func (m *mockJobRepo) UpdateOptions(ctx context.Context, id string, options model.JobOptions, updatedAt time.Time) error {
	return nil
}
func (m *mockJobRepo) UpdateRuns(ctx context.Context, id string, runs []model.Run, updatedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.jobs[id]; ok {
		j.Runs = runs
		m.jobs[id] = j
	}
	return nil
}
func (m *mockJobRepo) ListForDocument(ctx context.Context, documentID string) ([]model.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []model.Job
	for _, j := range m.jobs {
		if j.DocumentID == documentID {
			out = append(out, j)
		}
	}
	return out, nil
}
func (m *mockJobRepo) ListPending(ctx context.Context, stage string) ([]model.Job, error) {
	return nil, nil
}
func (m *mockJobRepo) ListPaginated(ctx context.Context, filter port.JobFilter, page model.PageRequest) (model.PageResult[model.Job], error) {
	return model.PageResult[model.Job]{}, nil
}
func (m *mockJobRepo) ResetRunning(ctx context.Context) (int, error) { return 0, nil }
func (m *mockJobRepo) CascadeReplay(ctx context.Context, documentID, fromStage string, stageOrder []string, updatedAt time.Time) error {
	return nil
}

type mockEventRepo struct {
	mu       sync.Mutex
	events   []model.StageEvent
	failures map[string]int // "docID:stage" → count
}

func newMockEventRepo() *mockEventRepo {
	return &mockEventRepo{failures: map[string]int{}}
}

func (m *mockEventRepo) Append(ctx context.Context, event model.StageEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	if event.EventType == model.EventFailed {
		key := event.DocumentID + ":" + event.Stage
		m.failures[key]++
	}
	return nil
}
func (m *mockEventRepo) CountFailures(ctx context.Context, documentID, stage string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failures[documentID+":"+stage], nil
}

type mockArtifactRepo struct {
	mu    sync.Mutex
	items map[string]model.Artifact
}

func (m *mockArtifactRepo) Insert(ctx context.Context, a model.Artifact) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.items == nil {
		m.items = map[string]model.Artifact{}
	}
	m.items[a.ID] = a
	return nil
}
func (m *mockArtifactRepo) Get(ctx context.Context, documentID, artifactID string) (model.Artifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.items[artifactID]; ok && a.DocumentID == documentID {
		return a, nil
	}
	return model.Artifact{}, fmt.Errorf("artifact not found: %s", artifactID)
}
func (m *mockArtifactRepo) ListForDocument(ctx context.Context, documentID string) ([]model.Artifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []model.Artifact
	for _, a := range m.items {
		if a.DocumentID == documentID {
			out = append(out, a)
		}
	}
	return out, nil
}

type mockContextRepo struct{}

func (m *mockContextRepo) List(ctx context.Context) ([]model.Context, error) { return nil, nil }
func (m *mockContextRepo) Create(ctx context.Context, name, text string) (model.Context, error) {
	return model.Context{}, nil
}
func (m *mockContextRepo) Update(ctx context.Context, id string, name, text *string) (model.Context, error) {
	return model.Context{}, nil
}
func (m *mockContextRepo) Delete(ctx context.Context, id string) (bool, error) { return false, nil }

type mockKVRepo struct {
	mu   sync.Mutex
	data map[string]string
}

func newMockKVRepo() *mockKVRepo { return &mockKVRepo{data: map[string]string{}} }
func (m *mockKVRepo) Set(ctx context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}
func (m *mockKVRepo) Get(ctx context.Context, key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	return v, ok, nil
}

type mockArtifactStore struct{ dir string }

func (m *mockArtifactStore) Save(vaultPath, artifactID, filename string, data []byte) error {
	dir := filepath.Join(vaultPath, "artifacts", artifactID)
	_ = os.MkdirAll(dir, 0755)
	return os.WriteFile(filepath.Join(dir, filename), data, 0644)
}
func (m *mockArtifactStore) Read(vaultPath, artifactID, filename string) ([]byte, error) {
	return os.ReadFile(filepath.Join(vaultPath, "artifacts", artifactID, filename))
}
func (m *mockArtifactStore) SaveAt(vaultPath, relPath string, data []byte) error {
	full := filepath.Join(vaultPath, relPath)
	_ = os.MkdirAll(filepath.Dir(full), 0755)
	return os.WriteFile(full, data, 0644)
}
func (m *mockArtifactStore) ReadAt(vaultPath, relPath string) ([]byte, error) {
	return os.ReadFile(filepath.Join(vaultPath, relPath))
}

type mockLLM struct {
	visionResponse string
	textResponse   string
	embedVector    []float32
	err            error
}

func (m *mockLLM) GenerateVision(ctx context.Context, model_, prompt string, imageBytes []byte, onChunk func(string)) error {
	if m.err != nil {
		return m.err
	}
	onChunk(m.visionResponse)
	return nil
}
func (m *mockLLM) GenerateText(ctx context.Context, model_, prompt string, onChunk func(string)) error {
	if m.err != nil {
		return m.err
	}
	onChunk(m.textResponse)
	return nil
}
func (m *mockLLM) ChatWithTools(ctx context.Context, model_ string, messages []port.LLMMessage, tools []port.LLMTool) (string, []port.LLMToolCall, error) {
	return m.textResponse, nil, m.err
}
func (m *mockLLM) ChatStream(ctx context.Context, model_ string, messages []port.LLMMessage, onChunk func(string)) error {
	return nil
}
func (m *mockLLM) GenerateEmbed(ctx context.Context, model_, text string) ([]float32, error) {
	return m.embedVector, m.err
}
func (m *mockLLM) Unload(ctx context.Context, model_ string) error { return nil }

type mockEmbedStore struct {
	upsertCount  int
	deleteCalled bool
}

func (m *mockEmbedStore) Upsert(ctx context.Context, id string, textVector, imageVector []float32, payload map[string]any) error {
	m.upsertCount++
	return nil
}
func (m *mockEmbedStore) Search(ctx context.Context, vector []float32, topK int) ([]port.EmbedResult, error) {
	return nil, nil
}
func (m *mockEmbedStore) DeleteByDocID(ctx context.Context, docID string) error {
	m.deleteCalled = true
	return nil
}
func (m *mockEmbedStore) GetByIDs(_ context.Context, _ []string) ([]port.EmbedResult, error) {
	return nil, nil
}
func (m *mockEmbedStore) DeleteBySeries(_ context.Context, _ string) error { return nil }

type mockStreamManager struct {
	mu     sync.Mutex
	events []port.StreamEvent
}

func (m *mockStreamManager) Publish(jobID string, event port.StreamEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
}
func (m *mockStreamManager) Subscribe(jobID string) <-chan port.StreamEvent {
	return make(chan port.StreamEvent)
}
func (m *mockStreamManager) Unsubscribe(jobID string) {}

type mockPromptRenderer struct{ response string }

func (m *mockPromptRenderer) Render(path string, data any) (string, error) {
	return m.response, nil
}

// ---- helpers ----

// seedFieldOutputs writes test fields to the worker's vault + artifact repo
// and returns the resulting model.Fields, suitable for attaching to a fake
// prior-stage Run that the tests use to drive `findInput` lookups.
func seedFieldOutputs(t *testing.T, w *WorkerService, docID, jobID string, fields ...[2]string) []model.Field {
	t.Helper()
	store := w.store.(*mockArtifactStore)
	repo := w.artifacts.(*mockArtifactRepo)
	runID := uuid.NewString()
	now := time.Now().UTC()
	var out []model.Field
	for _, f := range fields {
		name, text := f[0], f[1]
		relPath := runOutputPath(jobID, runID, name, "md")
		if err := store.SaveAt(w.vaultPath, relPath, []byte(text)); err != nil {
			t.Fatal(err)
		}
		artID := uuid.NewString()
		path := relPath
		if err := repo.Insert(context.Background(), model.Artifact{
			ID:          artID,
			DocumentID:  docID,
			Filename:    name + ".md",
			ContentType: "text/markdown",
			Path:        &path,
			CreatedAt:   now,
			UpdatedAt:   now,
		}); err != nil {
			t.Fatal(err)
		}
		out = append(out, model.Field{
			Field:      name,
			ArtifactID: artID,
			Size:       int64(len(text)),
			Preview:    previewOf(text),
		})
	}
	return out
}

// readOutputText resolves a Run output's text by reading the backing artifact.
func readOutputText(t *testing.T, w *WorkerService, docID string, f model.Field) string {
	t.Helper()
	if f.ArtifactID == "" {
		return ""
	}
	art, err := w.artifacts.Get(context.Background(), docID, f.ArtifactID)
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	text, err := readArtifactText(w.store, w.vaultPath, art)
	if err != nil {
		t.Fatalf("read artifact text: %v", err)
	}
	return text
}

func pngPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.png")
	// minimal valid PNG bytes
	_ = os.WriteFile(path, []byte("fake-png-bytes"), 0644)
	return path
}

func newWorker(t *testing.T, docs *mockDocRepo, jobs *mockJobRepo, events *mockEventRepo, llm *mockLLM, embed *mockEmbedStore, kv *mockKVRepo, prompts port.PromptRenderer, pipeline model.PipelineConfig) *WorkerService {
	t.Helper()
	return NewWorkerService(
		docs,
		jobs,
		&mockArtifactRepo{},
		events,
		&mockContextRepo{},
		kv,
		&mockArtifactStore{},
		llm,
		embed,
		&mockStreamManager{},
		prompts,
		session.InMemoryService(),
		pipeline,
		t.TempDir(),
	)
}

// ---- helper functions ----

func TestCheckContinueIf(t *testing.T) {
	stage := model.StageDefinition{
		ContinueIf: []map[string]any{{"confidence": "high"}},
	}

	if !checkContinueIf(stage, model.ConfidenceHigh) {
		t.Error("high should satisfy continue_if: high")
	}
	if checkContinueIf(stage, model.ConfidenceMedium) {
		t.Error("medium should not satisfy continue_if: high")
	}
	if checkContinueIf(stage, model.ConfidenceLow) {
		t.Error("low should not satisfy continue_if: high")
	}
}

func TestCheckContinueIf_NilRules(t *testing.T) {
	if !checkContinueIf(model.StageDefinition{}, model.ConfidenceLow) {
		t.Error("nil continue_if should always pass")
	}
}

func TestCheckStartIf_RequireContext(t *testing.T) {
	stage := model.StageDefinition{RequireContext: true}

	if !checkStartIf(model.Document{AdditionalContext: "some context"}, stage) {
		t.Error("doc with additional context should pass")
	}
	if !checkStartIf(model.Document{LinkedContexts: []string{"id"}}, stage) {
		t.Error("doc with linked contexts should pass")
	}
	if checkStartIf(model.Document{}, stage) {
		t.Error("doc without context should fail when require_context=true")
	}
}

func TestCheckStartIf_ContextProvidedRule(t *testing.T) {
	stage := model.StageDefinition{
		StartIf: map[string]any{"context_provided": true},
	}
	if checkStartIf(model.Document{}, stage) {
		t.Error("should fail when context_provided rule set and no context given")
	}
	if !checkStartIf(model.Document{AdditionalContext: "ctx"}, stage) {
		t.Error("should pass when context_provided rule set and context given")
	}
}

func TestIsSkipFileType(t *testing.T) {
	stage := model.StageDefinition{
		SkipIf: map[string]any{"file_type": []any{"txt", "md"}},
	}
	if !isSkipFileType(stage, model.FileTypeTXT) {
		t.Error("txt should be skipped")
	}
	if !isSkipFileType(stage, model.FileTypeMD) {
		t.Error("md should be skipped")
	}
	if isSkipFileType(stage, model.FileTypePNG) {
		t.Error("png should not be skipped")
	}
	if isSkipFileType(model.StageDefinition{}, model.FileTypeTXT) {
		t.Error("nil SkipIf should never skip")
	}
}

func TestFindInput(t *testing.T) {
	stageData := map[string]map[string]any{
		"ocr": {"ocr_raw": "hello world"},
	}

	text, field := findInput(stageData, "ocr_raw")
	if text != "hello world" || field != "ocr_raw" {
		t.Errorf("got text=%q field=%q", text, field)
	}

	text, field = findInput(stageData, "missing_field")
	if text != "" || field != "" {
		t.Errorf("missing field should return empty, got text=%q field=%q", text, field)
	}
}

// ---- chunkText ----

func TestChunkText(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		size    int
		overlap int
		want    int // expected chunk count
	}{
		{"short text fits in one chunk", "hello world", 1500, 200, 1},
		{"exact size", strings.Repeat("a", 1500), 1500, 200, 1},
		{"two chunks", strings.Repeat("a", 2000), 1500, 200, 2},
		{"three chunks", strings.Repeat("a", 4000), 1500, 200, 3},
		{"overlap preserves content", "abcdefghij", 6, 2, 2}, // step=4: [0:6],[4:10]
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chunks := chunkText(c.text, c.size, c.overlap)
			if len(chunks) != c.want {
				t.Errorf("got %d chunks, want %d", len(chunks), c.want)
			}
			// Verify no data loss: last chunk ends at text end.
			if len(chunks) > 0 && !strings.HasSuffix(c.text, chunks[len(chunks)-1]) {
				t.Error("last chunk does not end at text end")
			}
		})
	}
}

// ---- parseLLMResponse ----

func TestParseLLMResponse_JSON(t *testing.T) {
	raw := `{"tags": ["note", "meeting"], "summary": "A meeting note.", "confidence": "high"}`
	stage := model.StageDefinition{
		Outputs: []model.StageOutput{
			{Field: "tags", Type: "json_array"},
			{Field: "summary", Type: "text"},
		},
	}

	_, outputs, confidence, questions := parseLLMResponse(raw, "", "", stage)

	if confidence != model.ConfidenceHigh {
		t.Errorf("confidence: got %q, want %q", confidence, model.ConfidenceHigh)
	}
	if len(outputs) != 2 {
		t.Fatalf("outputs: got %d, want 2", len(outputs))
	}
	if len(questions) != 0 {
		t.Errorf("expected no questions, got %d", len(questions))
	}
}

func TestParseLLMResponse_JSONWithCodeFences(t *testing.T) {
	raw := "```json\n{\"confidence\": \"medium\", \"summary\": \"test\"}\n```"
	stage := model.StageDefinition{Output: "summary"}

	_, outputs, confidence, _ := parseLLMResponse(raw, "", "", stage)

	if confidence != model.ConfidenceMedium {
		t.Errorf("confidence: got %q", confidence)
	}
	if len(outputs) != 1 || outputs[0].text != "test" {
		t.Errorf("outputs: %+v", outputs)
	}
}

func TestParseLLMResponse_ClarifiedText(t *testing.T) {
	raw := `<clarified_text>
Hello world
</clarified_text>
<confidence>medium</confidence>
<questions>[]</questions>`

	stage := model.StageDefinition{Output: "clarified_text"}
	_, outputs, confidence, questions := parseLLMResponse(raw, "ocr_raw", "raw input", stage)

	if confidence != model.ConfidenceMedium {
		t.Errorf("confidence: got %q", confidence)
	}
	if len(outputs) != 1 || outputs[0].text != "Hello world" {
		t.Errorf("outputs: %+v", outputs)
	}
	if len(questions) != 0 {
		t.Errorf("expected no questions, got %d", len(questions))
	}
}

func TestParseLLMResponse_ClarifiedTextWithQuestions(t *testing.T) {
	raw := `<clarified_text>Some text</clarified_text>
<confidence>low</confidence>
<questions>[{"segment": "ambiguous part", "question": "What does this mean?"}]</questions>`

	stage := model.StageDefinition{Output: "clarified_text"}
	_, _, confidence, questions := parseLLMResponse(raw, "", "", stage)

	if confidence != model.ConfidenceLow {
		t.Errorf("confidence: got %q", confidence)
	}
	if len(questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(questions))
	}
	if questions[0].Segment != "ambiguous part" {
		t.Errorf("question segment: got %q", questions[0].Segment)
	}
}

func TestParseLLMResponse_ClarifiedTextStripsHTMLComments(t *testing.T) {
	raw := `<clarified_text><!-- comment -->Real content</clarified_text>
<confidence>high</confidence>
<questions>[]</questions>`

	stage := model.StageDefinition{Output: "clarified_text"}
	_, outputs, _, _ := parseLLMResponse(raw, "", "", stage)

	if len(outputs) != 1 || outputs[0].text != "Real content" {
		t.Errorf("HTML comment not stripped: %+v", outputs)
	}
}

func TestParseLLMResponse_InputPassthrough(t *testing.T) {
	raw := `{"confidence": "high", "summary": "ok"}`
	stage := model.StageDefinition{Output: "summary"}
	inputs, _, _, _ := parseLLMResponse(raw, "ocr_raw", "the input text", stage)

	if len(inputs) != 1 || inputs[0].field != "ocr_raw" || inputs[0].text != "the input text" {
		t.Errorf("inputs not passed through: %+v", inputs)
	}
}

// ---- processJob integration ----

func newTestDoc(pngPath string) model.Document {
	p := pngPath
	return model.Document{
		ID:          uuid.NewString(),
		ContentHash: "abc123",
		PNGPath:     &p,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
}

func newTestJob(docID, stage string) model.Job {
	return model.Job{
		ID:         uuid.NewString(),
		DocumentID: docID,
		Stage:      stage,
		Status:     model.JobStatusPending,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
}

func TestProcessJob_OCR_Success(t *testing.T) {
	png := pngPath(t)
	doc := newTestDoc(png)
	job := newTestJob(doc.ID, "ocr")

	docs := newMockDocRepo(doc)
	jobs := newMockJobRepo(job)
	events := newMockEventRepo()

	llm := &mockLLM{visionResponse: "# My Note\nSome transcribed text"}
	stage := model.StageDefinition{
		Name:    "ocr",
		Type:    model.StageTypeComputerVision,
		Model:   "llava",
		Outputs: []model.StageOutput{{Field: "ocr_raw", Type: "text"}},
	}
	pipeline := model.PipelineConfig{MaxConcurrent: 1, Stages: []model.StageDefinition{stage}}
	w := newWorker(t, docs, jobs, events, llm, &mockEmbedStore{}, newMockKVRepo(), &mockPromptRenderer{}, pipeline)

	w.processJob(context.Background(), job, stage)

	updatedJob, _ := jobs.GetByID(context.Background(), job.ID)
	if updatedJob.Status != model.JobStatusDone {
		t.Errorf("job status: got %q, want %q", updatedJob.Status, model.JobStatusDone)
	}
	if len(updatedJob.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(updatedJob.Runs))
	}
	if len(updatedJob.Runs[0].Outputs) == 0 || readOutputText(t, w, doc.ID, updatedJob.Runs[0].Outputs[0]) == "" {
		t.Error("expected OCR output text")
	}

	// Title should be set from first line of OCR output
	updatedDoc, _ := docs.Get(context.Background(), doc.ID)
	if updatedDoc.Title == nil || *updatedDoc.Title == "" {
		t.Error("expected doc title to be set from OCR output")
	}
}

func TestProcessJob_OCR_TextFileSkip(t *testing.T) {
	doc := model.Document{
		ID:        uuid.NewString(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	job := newTestJob(doc.ID, "ocr")

	kv := newMockKVRepo()
	meta, _ := json.Marshal(IngestMeta{RawText: "plain text content", FileType: model.FileTypeTXT})
	_ = kv.Set(context.Background(), kvIngestMetaPrefix+doc.ID, string(meta))

	docs := newMockDocRepo(doc)
	jobs := newMockJobRepo(job)
	events := newMockEventRepo()

	stage := model.StageDefinition{
		Name:    "ocr",
		Type:    model.StageTypeComputerVision,
		SkipIf:  map[string]any{"file_type": []any{"txt", "md"}},
		Outputs: []model.StageOutput{{Field: "ocr_raw", Type: "text"}},
	}
	pipeline := model.PipelineConfig{MaxConcurrent: 1, Stages: []model.StageDefinition{stage}}
	w := newWorker(t, docs, jobs, events, &mockLLM{}, &mockEmbedStore{}, kv, &mockPromptRenderer{}, pipeline)

	w.processJob(context.Background(), job, stage)

	updatedJob, _ := jobs.GetByID(context.Background(), job.ID)
	if updatedJob.Status != model.JobStatusDone {
		t.Errorf("job status: got %q, want done", updatedJob.Status)
	}
	// Verify OCR (vision model) was not called — run output should be the raw text
	if len(updatedJob.Runs) != 1 || readOutputText(t, w, doc.ID, updatedJob.Runs[0].Outputs[0]) != "plain text content" {
		t.Errorf("expected passthrough of raw text, got runs: %+v", updatedJob.Runs)
	}
}

func TestProcessJob_LLMText_WaitsForContext(t *testing.T) {
	doc := model.Document{
		ID:        uuid.NewString(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		// No AdditionalContext, no LinkedContexts
	}
	job := newTestJob(doc.ID, "clarify")

	docs := newMockDocRepo(doc)
	jobs := newMockJobRepo(job)
	events := newMockEventRepo()

	stage := model.StageDefinition{
		Name:           "clarify",
		Type:           model.StageTypeLLMText,
		RequireContext: true,
	}
	pipeline := model.PipelineConfig{MaxConcurrent: 1, Stages: []model.StageDefinition{stage}}
	w := newWorker(t, docs, jobs, events, &mockLLM{}, &mockEmbedStore{}, newMockKVRepo(), &mockPromptRenderer{}, pipeline)

	w.processJob(context.Background(), job, stage)

	updatedJob, _ := jobs.GetByID(context.Background(), job.ID)
	if updatedJob.Status != model.JobStatusWaiting {
		t.Errorf("job should be waiting for context, got %q", updatedJob.Status)
	}
}

func TestProcessJob_LLMText_LowConfidenceParkForReview(t *testing.T) {
	png := pngPath(t)
	doc := model.Document{
		ID:                uuid.NewString(),
		PNGPath:           &png,
		AdditionalContext: "some context",
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	job := newTestJob(doc.ID, "clarify")

	priorOCRJobID := uuid.NewString()
	jobs := newMockJobRepo(job)

	llmResp := `<clarified_text>Clarified output</clarified_text>
<confidence>low</confidence>
<questions>[{"segment": "unclear part", "question": "What is this?"}]</questions>
<document_context_update>ctx</document_context_update>`

	stage := model.StageDefinition{
		Name:       "clarify",
		Type:       model.StageTypeLLMText,
		Input:      "ocr_raw",
		Output:     "clarified_text",
		ContinueIf: []map[string]any{{"confidence": "high"}},
	}
	pipeline := model.PipelineConfig{MaxConcurrent: 1, Stages: []model.StageDefinition{stage}}
	w := newWorker(t,
		newMockDocRepo(doc),
		jobs,
		newMockEventRepo(),
		&mockLLM{textResponse: llmResp},
		&mockEmbedStore{},
		newMockKVRepo(),
		&mockPromptRenderer{},
		pipeline,
	)

	// Seed a prior OCR run via the worker's store so CollectStageData can read it.
	priorOutputs := seedFieldOutputs(t, w, doc.ID, priorOCRJobID, [2]string{"ocr_raw", "some ocr text"})
	_ = jobs.Upsert(context.Background(), model.Job{
		ID:         priorOCRJobID,
		DocumentID: doc.ID,
		Stage:      "ocr",
		Status:     model.JobStatusDone,
		Runs:       []model.Run{{ID: uuid.NewString(), Outputs: priorOutputs}},
	})

	w.processJob(context.Background(), job, stage)

	updatedJob, _ := jobs.GetByID(context.Background(), job.ID)
	if updatedJob.Status != model.JobStatusWaiting {
		t.Errorf("low confidence should park job for review, got %q", updatedJob.Status)
	}
	if len(updatedJob.Runs) != 1 || len(updatedJob.Runs[0].Questions) != 1 {
		t.Errorf("expected 1 run with 1 question: %+v", updatedJob.Runs)
	}
}

func TestProcessJob_Embed_Success(t *testing.T) {
	doc := model.Document{
		ID:        uuid.NewString(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	job := newTestJob(doc.ID, "embed")

	priorClarifyJobID := uuid.NewString()
	jobs := newMockJobRepo(job)

	embed := &mockEmbedStore{}
	stage := model.StageDefinition{
		Name:  "embed",
		Type:  model.StageTypeEmbed,
		Model: "nomic-embed-text",
		Input: "clarified_text",
	}
	pipeline := model.PipelineConfig{MaxConcurrent: 1, Stages: []model.StageDefinition{stage}}
	w := newWorker(t,
		newMockDocRepo(doc),
		jobs,
		newMockEventRepo(),
		&mockLLM{embedVector: []float32{0.1, 0.2, 0.3}},
		embed,
		newMockKVRepo(),
		&mockPromptRenderer{},
		pipeline,
	)

	priorOutputs := seedFieldOutputs(t, w, doc.ID, priorClarifyJobID, [2]string{"clarified_text", "embedded content"})
	_ = jobs.Upsert(context.Background(), model.Job{
		ID:         priorClarifyJobID,
		DocumentID: doc.ID,
		Stage:      "clarify",
		Status:     model.JobStatusDone,
		Runs:       []model.Run{{ID: uuid.NewString(), Outputs: priorOutputs}},
	})

	w.processJob(context.Background(), job, stage)

	if embed.upsertCount == 0 {
		t.Error("expected embed store Upsert to be called")
	}
	updatedJob, _ := jobs.GetByID(context.Background(), job.ID)
	if updatedJob.Status != model.JobStatusDone {
		t.Errorf("job status: got %q, want done", updatedJob.Status)
	}
}

func TestProcessJob_RetryOnFailure(t *testing.T) {
	doc := newTestDoc(t.TempDir() + "/missing.png") // PNG doesn't exist → OCR will fail
	job := newTestJob(doc.ID, "ocr")

	docs := newMockDocRepo(doc)
	jobs := newMockJobRepo(job)
	events := newMockEventRepo()

	stage := model.StageDefinition{
		Name:  "ocr",
		Type:  model.StageTypeComputerVision,
		Model: "llava",
	}
	pipeline := model.PipelineConfig{MaxConcurrent: 1, Stages: []model.StageDefinition{stage}}
	w := newWorker(t, docs, jobs, events, &mockLLM{}, &mockEmbedStore{}, newMockKVRepo(), &mockPromptRenderer{}, pipeline)

	w.processJob(context.Background(), job, stage)

	// First failure: should be reset to pending (failures < 3)
	updatedJob, _ := jobs.GetByID(context.Background(), job.ID)
	if updatedJob.Status != model.JobStatusPending {
		t.Errorf("first failure should reset to pending, got %q", updatedJob.Status)
	}
	if count, _ := events.CountFailures(context.Background(), doc.ID, "ocr"); count != 1 {
		t.Errorf("expected 1 failure event, got %d", count)
	}
}

func TestProcessJob_ExhaustsRetries(t *testing.T) {
	doc := newTestDoc(t.TempDir() + "/missing.png")
	job := newTestJob(doc.ID, "ocr")

	docs := newMockDocRepo(doc)
	jobs := newMockJobRepo(job)
	events := newMockEventRepo()

	// Pre-seed 3 failures so the next one exhausts retries
	for i := 0; i < 3; i++ {
		_ = events.Append(context.Background(), model.StageEvent{
			DocumentID: doc.ID, Stage: "ocr", EventType: model.EventFailed, Timestamp: time.Now(),
		})
	}

	stage := model.StageDefinition{Name: "ocr", Type: model.StageTypeComputerVision}
	pipeline := model.PipelineConfig{MaxConcurrent: 1, Stages: []model.StageDefinition{stage}}
	w := newWorker(t, docs, jobs, events, &mockLLM{}, &mockEmbedStore{}, newMockKVRepo(), &mockPromptRenderer{}, pipeline)

	w.processJob(context.Background(), job, stage)

	updatedJob, _ := jobs.GetByID(context.Background(), job.ID)
	if updatedJob.Status != model.JobStatusError {
		t.Errorf("3 failures should set status to error, got %q", updatedJob.Status)
	}
}
