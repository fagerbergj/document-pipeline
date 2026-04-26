package core_test

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/fagerbergj/document-pipeline/server/core"
	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/fagerbergj/document-pipeline/server/store/filesystem"
	"github.com/fagerbergj/document-pipeline/server/store/postgres"
)

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(root, "db", "migrations")
}

var (
	sharedDSNOnce sync.Once
	sharedDSN     string
	sharedDSNErr  error
)

func ensureSharedPostgres(t *testing.T) string {
	t.Helper()
	sharedDSNOnce.Do(func() {
		ctx := context.Background()
		ctr, err := tcpostgres.Run(ctx,
			"postgres:17-alpine",
			tcpostgres.WithDatabase("testdb"),
			tcpostgres.WithUsername("test"),
			tcpostgres.WithPassword("test"),
			tcpostgres.BasicWaitStrategies(),
			tcpostgres.WithSQLDriver("pgx"),
		)
		if err != nil {
			sharedDSNErr = fmt.Errorf("start postgres container: %w", err)
			return
		}
		dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			sharedDSNErr = fmt.Errorf("connection string: %w", err)
			return
		}
		sharedDSN = dsn
	})
	if sharedDSNErr != nil {
		t.Fatal(sharedDSNErr)
	}
	return sharedDSN
}

func openTestDB(t *testing.T) *postgres.DB {
	t.Helper()
	base := ensureSharedPostgres(t)

	schema := fmt.Sprintf("test_%d_%d", time.Now().UnixNano(), rand.Intn(1<<16))
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	dsn := base + sep + "search_path=" + schema

	db, err := postgres.Open(dsn, migrationsDir(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		admin, err := sql.Open("pgx", base)
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	})
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
func (m *mockIndexer) Search(_ context.Context, _ string, _, _ int) ([]string, int, error) {
	return nil, 0, nil
}

func seedDocument(t *testing.T, db *postgres.DB, id string) {
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

	idx := &mockIndexer{count: 0}
	svc := core.NewIndexerService(db.DB(), db.Documents(), db.Jobs(), db.Artifacts(), filesystem.New(), idx, t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go svc.Run(ctx)

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

	idx := &mockIndexer{count: 5}
	svc := core.NewIndexerService(db.DB(), db.Documents(), db.Jobs(), db.Artifacts(), filesystem.New(), idx, t.TempDir())

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

	_, err := db.DB().ExecContext(context.Background(),
		"INSERT INTO index_queue (doc_id, action) VALUES ($1, 'delete')", "doc-del")
	if err != nil {
		t.Fatalf("insert queue entry: %v", err)
	}

	idx := &mockIndexer{count: 1}
	svc := core.NewIndexerService(db.DB(), db.Documents(), db.Jobs(), db.Artifacts(), filesystem.New(), idx, t.TempDir())

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

	for i := 0; i < 3; i++ {
		_, err := db.DB().ExecContext(context.Background(),
			"INSERT INTO index_queue (doc_id, action) VALUES ($1, 'index')", "doc-dup")
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	idx := &mockIndexer{count: 1}
	svc := core.NewIndexerService(db.DB(), db.Documents(), db.Jobs(), db.Artifacts(), filesystem.New(), idx, t.TempDir())

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

	time.Sleep(100 * time.Millisecond)

	idx.mu.Lock()
	got := len(idx.indexed)
	idx.mu.Unlock()

	if got != 1 {
		t.Errorf("expected 1 index call for 3 duplicate entries, got %d", got)
	}
}
