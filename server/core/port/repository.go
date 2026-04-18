package port

import (
	"context"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/model"
)

// DocumentRepo persists and retrieves documents.
type DocumentRepo interface {
	Insert(ctx context.Context, doc model.Document) error
	Get(ctx context.Context, id string) (model.Document, error)
	GetByHash(ctx context.Context, hash string) (model.Document, bool, error)
	Update(ctx context.Context, doc model.Document) error
	Delete(ctx context.Context, id string) error
	ListPaginated(ctx context.Context, filter DocumentFilter, page model.PageRequest) (model.PageResult[model.Document], error)
	ListBySeries(ctx context.Context, series string) ([]model.Document, error)
}

type DocumentFilter struct {
	Stages   []string
	Statuses []string
	Sort     string
}

// JobRepo persists and retrieves jobs.
type JobRepo interface {
	Upsert(ctx context.Context, job model.Job) error
	GetByID(ctx context.Context, id string) (model.Job, error)
	GetByDocumentAndStage(ctx context.Context, documentID, stage string) (model.Job, bool, error)
	UpdateStatus(ctx context.Context, id, status string, updatedAt time.Time) error
	UpdateOptions(ctx context.Context, id string, options model.JobOptions, updatedAt time.Time) error
	UpdateRuns(ctx context.Context, id string, runs []model.Run, updatedAt time.Time) error
	ListForDocument(ctx context.Context, documentID string) ([]model.Job, error)
	ListPending(ctx context.Context, stage string) ([]model.Job, error)
	ListPaginated(ctx context.Context, filter JobFilter, page model.PageRequest) (model.PageResult[model.Job], error)
	ResetRunning(ctx context.Context) (int, error)
	CascadeReplay(ctx context.Context, documentID, fromStage string, stageOrder []string, updatedAt time.Time) error
}

type JobFilter struct {
	IDs         []string
	DocumentIDs []string
	Stages      []string
	Statuses    []string
	Sort        string
}

// ArtifactRepo persists and retrieves artifacts.
type ArtifactRepo interface {
	Insert(ctx context.Context, artifact model.Artifact) error
	Get(ctx context.Context, documentID, artifactID string) (model.Artifact, error)
	ListForDocument(ctx context.Context, documentID string) ([]model.Artifact, error)
}

// StageEventRepo appends to the audit log and queries failure counts.
type StageEventRepo interface {
	Append(ctx context.Context, event model.StageEvent) error
	CountFailures(ctx context.Context, documentID, stage string) (int, error)
}

// ContextRepo manages reusable context snippets.
type ContextRepo interface {
	List(ctx context.Context) ([]model.Context, error)
	Create(ctx context.Context, name, text string) (model.Context, error)
	Update(ctx context.Context, id string, name, text *string) (model.Context, error)
	Delete(ctx context.Context, id string) (bool, error)
}

// ChatRepo manages chat sessions.
type ChatRepo interface {
	Create(ctx context.Context, systemPrompt string, rag model.RAGConfig) (model.ChatSession, error)
	Get(ctx context.Context, id string) (model.ChatSession, bool, error)
	Update(ctx context.Context, id string, updates ChatSessionUpdates) (model.ChatSession, error)
	Delete(ctx context.Context, id string) (bool, error)
	List(ctx context.Context, pageSize int, beforeID *string) ([]model.ChatSession, error)
}

type ChatSessionUpdates struct {
	Title        *string
	SystemPrompt *string
	RAGRetrieval *model.RAGConfig
}

// ChatMessageRepo manages messages within a chat session.
type ChatMessageRepo interface {
	Append(ctx context.Context, sessionID, role, content string, sources []model.SourceRef) (model.ChatMessage, error)
	List(ctx context.Context, sessionID string) ([]model.ChatMessage, error)
}

// KeyValueRepo is a simple persistent key-value store.
type KeyValueRepo interface {
	Set(ctx context.Context, key, value string) error
	Get(ctx context.Context, key string) (string, bool, error)
}
