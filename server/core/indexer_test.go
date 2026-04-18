package core_test

import (
	"context"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core"
	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/fagerbergj/document-pipeline/server/store/sqlite"
)

// migrationsDir returns the absolute path to db/migrations.
func migrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile is .../server/core/indexer_test.go
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(root, "db", "migrations")
}

func openTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(t.TempDir()+"/test.db", migrationsDir(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// mockIndexer records calls to Index and Delete.
type mockIndexer struct {
	mu      sync.Mutex
	indexed []port.IndexDoc
	deleted []string
	count   int
}

func (m *mockIndexer) EnsureIndex(_ context.Context) error { return nil }
func (m *mockIndexer) Count(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count, nil
}
func (m *mockIndexer) Index(_ context.Context, doc port.IndexDoc) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.indexed = append(m.indexed, doc)
	return nil
}
func (m *mockIndexer) Delete(_ context.Context, docID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted = append(m.deleted, docID)
	return nil
}
func (m *mockIndexer) Search(_ context.Context, _ string, _ int) ([]string, error) {
	return nil, nil
}

func seedDocument(t *testing.T, db *sqlite.DB, id string) {
	t.Helper()
	err := db.Documents().Insert(context.Background(), model.Document{
		ID:             id,
		ContentHash:    "hash-" + id,
		LinkedContexts: []string{},
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seedDocument %s: %v", id, err)
	}
}

func TestIndexerService_BackfillIfEmpty(t *testing.T) {
	db := openTestDB(t)
	seedDocument(t, db, "doc-1")
	seedDocument(t, db, "doc-2")

	idx := &mockIndexer{count: 0} // empty index triggers backfill
	svc := core.NewIndexerService(db.DB(), db.Documents(), db.Jobs(), idx)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run one cycle: backfill enqueues docs, then processQueue indexes them.
	go svc.Run(ctx)

	// Wait for both docs to be indexed (up to 4s).
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		idx.mu.Lock()
		n := len(idx.indexed)
		idx.mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	idx.mu.Lock()
	got := len(idx.indexed)
	idx.mu.Unlock()

	if got < 2 {
		t.Errorf("expected 2 docs indexed after backfill, got %d", got)
	}
}

func TestIndexerService_BackfillSkippedWhenNonEmpty(t *testing.T) {
	db := openTestDB(t)
	seedDocument(t, db, "doc-1")

	idx := &mockIndexer{count: 5} // non-empty index — skip backfill
	svc := core.NewIndexerService(db.DB(), db.Documents(), db.Jobs(), idx)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	svc.Run(ctx)

	idx.mu.Lock()
	got := len(idx.indexed)
	idx.mu.Unlock()

	if got != 0 {
		t.Errorf("expected 0 index calls when index non-empty, got %d", got)
	}
}

func TestIndexerService_ProcessQueue_Delete(t *testing.T) {
	db := openTestDB(t)
	seedDocument(t, db, "doc-del")

	// Manually insert a delete action into index_queue.
	_, err := db.DB().ExecContext(context.Background(),
		"INSERT INTO index_queue (doc_id, action) VALUES (?, 'delete')", "doc-del")
	if err != nil {
		t.Fatalf("insert queue entry: %v", err)
	}

	idx := &mockIndexer{count: 1} // non-empty so backfill skipped
	svc := core.NewIndexerService(db.DB(), db.Documents(), db.Jobs(), idx)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go svc.Run(ctx)

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		idx.mu.Lock()
		n := len(idx.deleted)
		idx.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	idx.mu.Lock()
	deleted := idx.deleted
	idx.mu.Unlock()

	if len(deleted) != 1 || deleted[0] != "doc-del" {
		t.Errorf("expected doc-del deleted, got %v", deleted)
	}
}

func TestIndexerService_Deduplication(t *testing.T) {
	db := openTestDB(t)
	seedDocument(t, db, "doc-dup")

	// Insert three index entries for the same doc — only one Index call expected.
	for i := 0; i < 3; i++ {
		_, err := db.DB().ExecContext(context.Background(),
			"INSERT INTO index_queue (doc_id, action) VALUES (?, 'index')", "doc-dup")
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	idx := &mockIndexer{count: 1}
	svc := core.NewIndexerService(db.DB(), db.Documents(), db.Jobs(), idx)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go svc.Run(ctx)

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		idx.mu.Lock()
		n := len(idx.indexed)
		idx.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Give it an extra tick to ensure no duplicates.
	time.Sleep(100 * time.Millisecond)

	idx.mu.Lock()
	got := len(idx.indexed)
	idx.mu.Unlock()

	if got != 1 {
		t.Errorf("expected 1 index call for 3 duplicate entries, got %d", got)
	}
}
