package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core"
	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
)

func ts() time.Time { return time.Now().UTC().Truncate(time.Second) }

// seedDoc inserts a minimal document for use in job/artifact tests.
func seedDoc(t *testing.T, repo *DocumentRepo, id string) {
	t.Helper()
	err := repo.Insert(context.Background(), model.Document{
		ID:             id,
		ContentHash:    "hash-" + id,
		LinkedContexts: []string{},
		CreatedAt:      ts(),
		UpdatedAt:      ts(),
	})
	if err != nil {
		t.Fatalf("seedDoc %s: %v", id, err)
	}
}

// ── Document tests ────────────────────────────────────────────────────────────

func TestDocumentRepo_InsertGet(t *testing.T) {
	repo := openTestDB(t).Documents()
	ctx := context.Background()

	title := "Test Doc"
	doc := model.Document{
		ID:                "doc-1",
		ContentHash:       "abc123",
		Title:             &title,
		AdditionalContext: "ctx",
		LinkedContexts:    []string{"c1", "c2"},
		CreatedAt:         ts(),
		UpdatedAt:         ts(),
	}
	if err := repo.Insert(ctx, doc); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, "doc-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "doc-1" {
		t.Errorf("ID: got %q", got.ID)
	}
	if got.Title == nil || *got.Title != title {
		t.Errorf("Title: got %v", got.Title)
	}
	if len(got.LinkedContexts) != 2 {
		t.Errorf("LinkedContexts: got %v", got.LinkedContexts)
	}
}

func TestDocumentRepo_GetByHash(t *testing.T) {
	repo := openTestDB(t).Documents()
	ctx := context.Background()

	repo.Insert(ctx, model.Document{ID: "doc-1", ContentHash: "hash-abc", LinkedContexts: []string{}, CreatedAt: ts(), UpdatedAt: ts()})

	got, found, err := repo.GetByHash(ctx, "hash-abc")
	if err != nil || !found {
		t.Fatalf("GetByHash found=%v err=%v", found, err)
	}
	if got.ID != "doc-1" {
		t.Errorf("ID: got %q", got.ID)
	}

	_, found2, err2 := repo.GetByHash(ctx, "nonexistent")
	if err2 != nil || found2 {
		t.Errorf("nonexistent: found=%v err=%v", found2, err2)
	}
}

func TestDocumentRepo_Update(t *testing.T) {
	repo := openTestDB(t).Documents()
	ctx := context.Background()

	doc := model.Document{ID: "doc-1", ContentHash: "h1", LinkedContexts: []string{}, CreatedAt: ts(), UpdatedAt: ts()}
	repo.Insert(ctx, doc)

	newTitle := "Updated"
	doc.Title = &newTitle
	doc.UpdatedAt = ts()
	if err := repo.Update(ctx, doc); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _ := repo.Get(ctx, "doc-1")
	if got.Title == nil || *got.Title != "Updated" {
		t.Errorf("Title after update: %v", got.Title)
	}
}

func TestDocumentRepo_Delete(t *testing.T) {
	repo := openTestDB(t).Documents()
	ctx := context.Background()

	repo.Insert(ctx, model.Document{ID: "doc-1", ContentHash: "h1", LinkedContexts: []string{}, CreatedAt: ts(), UpdatedAt: ts()})
	if err := repo.Delete(ctx, "doc-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, "doc-1"); err == nil {
		t.Error("expected error after delete")
	}
}

func TestDocumentRepo_ListPaginated(t *testing.T) {
	repo := openTestDB(t).Documents()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		repo.Insert(ctx, model.Document{
			ID:             "doc-" + string(rune('a'+i)),
			ContentHash:    "h" + string(rune('a'+i)),
			LinkedContexts: []string{},
			CreatedAt:      ts().Add(time.Duration(i) * time.Second),
			UpdatedAt:      ts(),
		})
	}

	// Page 1: 3 items
	p1, err := repo.ListPaginated(ctx, port.DocumentFilter{Sort: "pipeline"}, model.PageRequest{PageSize: 3})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(p1.Data) != 3 {
		t.Errorf("page 1 len: got %d, want 3", len(p1.Data))
	}
	if p1.NextPageToken == nil {
		t.Fatal("expected next page token on page 1")
	}

	// Decode token and fetch page 2
	tok, err := core.DecodePageToken(*p1.NextPageToken)
	if err != nil {
		t.Fatalf("decode page token: %v", err)
	}
	p2, err := repo.ListPaginated(ctx, port.DocumentFilter{Sort: "pipeline"}, model.PageRequest{PageSize: 3, PageToken: &tok})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(p2.Data) != 2 {
		t.Errorf("page 2 len: got %d, want 2", len(p2.Data))
	}
	if p2.NextPageToken != nil {
		t.Error("page 2 should have no next token")
	}
}

// ── Job tests ─────────────────────────────────────────────────────────────────

func TestJobRepo_UpsertGet(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedDoc(t, db.Documents(), "doc-1")
	repo := db.Jobs()

	job := model.Job{
		ID:         "job-1",
		DocumentID: "doc-1",
		Stage:      "ocr",
		Status:     model.JobStatusPending,
		Options:    model.JobOptions{RequireContext: true},
		Runs:       []model.Run{},
		CreatedAt:  ts(),
		UpdatedAt:  ts(),
	}
	if err := repo.Upsert(ctx, job); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := repo.GetByID(ctx, "job-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != model.JobStatusPending {
		t.Errorf("Status: got %q", got.Status)
	}
	if !got.Options.RequireContext {
		t.Error("Options.RequireContext should be true")
	}
}

func TestJobRepo_GetByDocumentAndStage(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedDoc(t, db.Documents(), "doc-1")
	repo := db.Jobs()

	repo.Upsert(ctx, model.Job{ID: "j1", DocumentID: "doc-1", Stage: "ocr", Status: model.JobStatusPending, CreatedAt: ts(), UpdatedAt: ts()})

	job, found, err := repo.GetByDocumentAndStage(ctx, "doc-1", "ocr")
	if err != nil || !found {
		t.Fatalf("GetByDocumentAndStage: found=%v err=%v", found, err)
	}
	if job.Stage != "ocr" {
		t.Errorf("Stage: got %q", job.Stage)
	}

	_, found2, _ := repo.GetByDocumentAndStage(ctx, "doc-1", "embed")
	if found2 {
		t.Error("embed stage should not exist")
	}
}

func TestJobRepo_UpdateStatus(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedDoc(t, db.Documents(), "doc-1")
	repo := db.Jobs()

	repo.Upsert(ctx, model.Job{ID: "j1", DocumentID: "doc-1", Stage: "ocr", Status: model.JobStatusPending, CreatedAt: ts(), UpdatedAt: ts()})
	if err := repo.UpdateStatus(ctx, "j1", "running", ts()); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ := repo.GetByID(ctx, "j1")
	if got.Status != model.JobStatusRunning {
		t.Errorf("Status: got %q", got.Status)
	}
}

func TestJobRepo_UpdateRuns(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedDoc(t, db.Documents(), "doc-1")
	repo := db.Jobs()

	repo.Upsert(ctx, model.Job{ID: "j1", DocumentID: "doc-1", Stage: "ocr", Status: model.JobStatusPending, CreatedAt: ts(), UpdatedAt: ts()})

	runs := []model.Run{{
		ID:         "r1",
		Inputs:     []model.Field{{Field: "in", Text: "hello"}},
		Outputs:    []model.Field{{Field: "out", Text: "world"}},
		Confidence: model.ConfidenceHigh,
		Questions:  []model.Question{},
		CreatedAt:  ts(),
		UpdatedAt:  ts(),
	}}
	if err := repo.UpdateRuns(ctx, "j1", runs, ts()); err != nil {
		t.Fatalf("UpdateRuns: %v", err)
	}
	got, _ := repo.GetByID(ctx, "j1")
	if len(got.Runs) != 1 || got.Runs[0].ID != "r1" {
		t.Errorf("Runs: got %+v", got.Runs)
	}
}

func TestJobRepo_ListForDocument(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedDoc(t, db.Documents(), "doc-1")
	repo := db.Jobs()

	for _, stage := range []string{"ocr", "clarify"} {
		repo.Upsert(ctx, model.Job{ID: stage + "-j", DocumentID: "doc-1", Stage: stage, Status: model.JobStatusPending, CreatedAt: ts(), UpdatedAt: ts()})
	}

	jobs, err := repo.ListForDocument(ctx, "doc-1")
	if err != nil || len(jobs) != 2 {
		t.Errorf("ListForDocument: len=%d err=%v", len(jobs), err)
	}
}

func TestJobRepo_ResetRunning(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedDoc(t, db.Documents(), "doc-1")
	repo := db.Jobs()

	repo.Upsert(ctx, model.Job{ID: "j1", DocumentID: "doc-1", Stage: "ocr", Status: model.JobStatusRunning, CreatedAt: ts(), UpdatedAt: ts()})
	n, err := repo.ResetRunning(ctx)
	if err != nil || n != 1 {
		t.Errorf("ResetRunning: n=%d err=%v", n, err)
	}
	got, _ := repo.GetByID(ctx, "j1")
	if got.Status != model.JobStatusPending {
		t.Errorf("Status after reset: %q", got.Status)
	}
}

func TestJobRepo_CascadeReplay(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedDoc(t, db.Documents(), "doc-1")
	repo := db.Jobs()

	stages := []string{"ocr", "clarify", "classify", "embed"}
	for _, s := range stages {
		repo.Upsert(ctx, model.Job{ID: s + "-j", DocumentID: "doc-1", Stage: s, Status: model.JobStatusDone, CreatedAt: ts(), UpdatedAt: ts()})
	}

	if err := repo.CascadeReplay(ctx, "doc-1", "clarify", stages, ts()); err != nil {
		t.Fatalf("CascadeReplay: %v", err)
	}

	wantStatus := map[string]model.JobStatus{
		"ocr":      model.JobStatusDone,
		"clarify":  model.JobStatusDone,
		"classify": model.JobStatusPending,
		"embed":    model.JobStatusPending,
	}
	for stage, want := range wantStatus {
		job, found, _ := repo.GetByDocumentAndStage(ctx, "doc-1", stage)
		if !found {
			t.Errorf("stage %q not found", stage)
			continue
		}
		if job.Status != want {
			t.Errorf("stage %q: got %q, want %q", stage, job.Status, want)
		}
	}
}

func TestJobRepo_Upsert_UpdatesExisting(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedDoc(t, db.Documents(), "doc-1")
	repo := db.Jobs()

	// Insert first job for doc-1/ocr
	repo.Upsert(ctx, model.Job{ID: "j1", DocumentID: "doc-1", Stage: "ocr", Status: model.JobStatusPending, CreatedAt: ts(), UpdatedAt: ts()})
	// Upsert second job with same doc-1/ocr — updates in place
	repo.Upsert(ctx, model.Job{ID: "j2", DocumentID: "doc-1", Stage: "ocr", Status: model.JobStatusRunning, CreatedAt: ts(), UpdatedAt: ts()})

	// The original ID should now have running status
	got, _ := repo.GetByID(ctx, "j1")
	if got.Status != model.JobStatusRunning {
		t.Errorf("Status after upsert: got %q, want running", got.Status)
	}
}

func TestJobRepo_ListPaginated(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedDoc(t, db.Documents(), "doc-1")
	repo := db.Jobs()

	for i, stage := range []string{"ocr", "clarify", "classify"} {
		repo.Upsert(ctx, model.Job{
			ID:         stage + "-j",
			DocumentID: "doc-1",
			Stage:      stage,
			Status:     model.JobStatusDone,
			CreatedAt:  ts().Add(time.Duration(i) * time.Second),
			UpdatedAt:  ts(),
		})
	}

	p1, err := repo.ListPaginated(ctx, port.JobFilter{Sort: "pipeline"}, model.PageRequest{PageSize: 2})
	if err != nil || len(p1.Data) != 2 {
		t.Errorf("page 1: len=%d err=%v", len(p1.Data), err)
	}
	if p1.NextPageToken == nil {
		t.Fatal("expected page token")
	}

	tok, _ := core.DecodePageToken(*p1.NextPageToken)
	p2, err := repo.ListPaginated(ctx, port.JobFilter{Sort: "pipeline"}, model.PageRequest{PageSize: 2, PageToken: &tok})
	if err != nil || len(p2.Data) != 1 {
		t.Errorf("page 2: len=%d err=%v", len(p2.Data), err)
	}
}

// ── Artifact tests ────────────────────────────────────────────────────────────

func TestArtifactRepo(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedDoc(t, db.Documents(), "doc-1")
	repo := db.Artifacts()

	a := model.Artifact{ID: "art-1", DocumentID: "doc-1", Filename: "img.png", ContentType: "image/png", CreatedAt: ts(), UpdatedAt: ts()}
	if err := repo.Insert(ctx, a); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, "doc-1", "art-1")
	if err != nil || got.Filename != "img.png" {
		t.Errorf("Get: %v err=%v", got.Filename, err)
	}

	list, err := repo.ListForDocument(ctx, "doc-1")
	if err != nil || len(list) != 1 {
		t.Errorf("List: len=%d err=%v", len(list), err)
	}
}

// ── StageEvent tests ──────────────────────────────────────────────────────────

func TestStageEventRepo(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedDoc(t, db.Documents(), "doc-1")
	repo := db.StageEvents()

	for i := 0; i < 3; i++ {
		repo.Append(ctx, model.StageEvent{DocumentID: "doc-1", Stage: "ocr", EventType: model.EventFailed, Timestamp: ts()})
	}

	count, err := repo.CountFailures(ctx, "doc-1", "ocr")
	if err != nil || count != 3 {
		t.Errorf("CountFailures ocr: got %d err=%v", count, err)
	}
	count2, _ := repo.CountFailures(ctx, "doc-1", "clarify")
	if count2 != 0 {
		t.Errorf("CountFailures clarify: got %d, want 0", count2)
	}
}

// ── Context tests ─────────────────────────────────────────────────────────────

func TestContextRepo(t *testing.T) {
	repo := openTestDB(t).Contexts()
	ctx := context.Background()

	e, err := repo.Create(ctx, "My Context", "Some text")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if e.Name != "My Context" {
		t.Errorf("Name: %q", e.Name)
	}

	list, _ := repo.List(ctx)
	if len(list) != 1 {
		t.Errorf("List: got %d", len(list))
	}

	newName := "Updated"
	updated, err := repo.Update(ctx, e.ID, &newName, nil)
	if err != nil || updated.Name != "Updated" {
		t.Errorf("Update: name=%q err=%v", updated.Name, err)
	}

	deleted, err := repo.Delete(ctx, e.ID)
	if err != nil || !deleted {
		t.Errorf("Delete: deleted=%v err=%v", deleted, err)
	}
	list2, _ := repo.List(ctx)
	if len(list2) != 0 {
		t.Errorf("List after delete: got %d", len(list2))
	}
}

// ── KeyValue tests ────────────────────────────────────────────────────────────

func TestKeyValueRepo(t *testing.T) {
	repo := openTestDB(t).KeyValues()
	ctx := context.Background()

	repo.Set(ctx, "k1", "v1")
	v, ok, err := repo.Get(ctx, "k1")
	if err != nil || !ok || v != "v1" {
		t.Errorf("Get: v=%q ok=%v err=%v", v, ok, err)
	}

	repo.Set(ctx, "k1", "v2")
	v2, _, _ := repo.Get(ctx, "k1")
	if v2 != "v2" {
		t.Errorf("overwrite: got %q", v2)
	}

	_, ok2, _ := repo.Get(ctx, "missing")
	if ok2 {
		t.Error("missing key should return ok=false")
	}
}

// ── IDs filter ────────────────────────────────────────────────────────────────

func TestDocumentRepo_ListPaginated_IDsFilter(t *testing.T) {
	repo := openTestDB(t).Documents()
	ctx := context.Background()

	ids := []string{"doc-a", "doc-b", "doc-c", "doc-d"}
	for i, id := range ids {
		repo.Insert(ctx, model.Document{
			ID:             id,
			ContentHash:    "h" + string(rune('a'+i)),
			LinkedContexts: []string{},
			CreatedAt:      ts(),
			UpdatedAt:      ts(),
		})
	}

	result, err := repo.ListPaginated(ctx,
		port.DocumentFilter{Sort: "pipeline", IDs: []string{"doc-b", "doc-d"}},
		model.PageRequest{PageSize: 20},
	)
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	if len(result.Data) != 2 {
		t.Fatalf("want 2 results, got %d", len(result.Data))
	}
	got := map[string]bool{}
	for _, d := range result.Data {
		got[d.ID] = true
	}
	if !got["doc-b"] || !got["doc-d"] {
		t.Errorf("unexpected result set: %v", got)
	}
	// IDs filter returns no pagination token.
	if result.NextPageToken != nil {
		t.Error("expected no next_page_token when IDs filter active")
	}
}

func TestDocumentRepo_ListPaginated_IDsFilter_Empty(t *testing.T) {
	repo := openTestDB(t).Documents()
	ctx := context.Background()

	repo.Insert(ctx, model.Document{ID: "doc-1", ContentHash: "h1", LinkedContexts: []string{}, CreatedAt: ts(), UpdatedAt: ts()})

	result, err := repo.ListPaginated(ctx,
		port.DocumentFilter{Sort: "pipeline", IDs: []string{"nonexistent"}},
		model.PageRequest{PageSize: 20},
	)
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	if len(result.Data) != 0 {
		t.Errorf("want 0 results for unknown ID, got %d", len(result.Data))
	}
}
