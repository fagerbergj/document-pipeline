package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/google/uuid"
)

// ChatRepo implements port.ChatRepo against SQLite.
type ChatRepo struct{ db *sql.DB }

var _ port.ChatRepo = (*ChatRepo)(nil)

func (r *ChatRepo) Create(ctx context.Context, systemPrompt string, rag model.RAGConfig) (model.ChatSession, error) {
	id := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	ragJSON, err := json.Marshal(rag)
	if err != nil {
		return model.ChatSession{}, err
	}
	_, err = r.db.ExecContext(ctx, q["chat.SessionCreate"],
		id, systemPrompt, string(ragJSON), now, now)
	if err != nil {
		return model.ChatSession{}, err
	}
	t, _ := time.Parse(time.RFC3339Nano, now)
	return model.ChatSession{
		ID:           id,
		SystemPrompt: systemPrompt,
		RAGRetrieval: rag,
		CreatedAt:    t,
		UpdatedAt:    t,
	}, nil
}

func (r *ChatRepo) Get(ctx context.Context, id string) (model.ChatSession, bool, error) {
	row := r.db.QueryRowContext(ctx, q["chat.SessionGet"], id)
	s, err := scanChatSession(row)
	if err == sql.ErrNoRows {
		return model.ChatSession{}, false, nil
	}
	if err != nil {
		return model.ChatSession{}, false, err
	}
	return s, true, nil
}

func (r *ChatRepo) Update(ctx context.Context, id string, u port.ChatSessionUpdates) (model.ChatSession, error) {
	sets := []string{}
	params := []any{}
	if u.Title != nil {
		sets = append(sets, "title=?")
		params = append(params, *u.Title)
	}
	if u.SystemPrompt != nil {
		sets = append(sets, "system_prompt=?")
		params = append(params, *u.SystemPrompt)
	}
	if u.RAGRetrieval != nil {
		b, _ := json.Marshal(*u.RAGRetrieval)
		sets = append(sets, "rag_retrieval=?")
		params = append(params, string(b))
	}
	if len(sets) > 0 {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		sets = append(sets, "updated_at=?")
		params = append(params, now)
		params = append(params, id)
		_, err := r.db.ExecContext(ctx,
			"UPDATE chat_sessions SET "+strings.Join(sets, ", ")+" WHERE id=?", params...)
		if err != nil {
			return model.ChatSession{}, err
		}
	}

	row := r.db.QueryRowContext(ctx, q["chat.SessionGet"], id)
	s, err := scanChatSession(row)
	if err == sql.ErrNoRows {
		return model.ChatSession{}, fmt.Errorf("chat not found: %s", id)
	}
	return s, err
}

func (r *ChatRepo) Delete(ctx context.Context, id string) (bool, error) {
	res, err := r.db.ExecContext(ctx, q["chat.SessionDelete"], id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (r *ChatRepo) List(ctx context.Context, pageSize int, beforeID *string) ([]model.ChatSession, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if beforeID != nil {
		var refCreatedAt string
		r.db.QueryRowContext(ctx, q["chat.SessionGetCreatedAt"], *beforeID).Scan(&refCreatedAt)
		rows, err = r.db.QueryContext(ctx, q["chat.SessionListBefore"], refCreatedAt, *beforeID, pageSize)
	} else {
		rows, err = r.db.QueryContext(ctx, q["chat.SessionListNoFilter"], pageSize)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []model.ChatSession
	for rows.Next() {
		s, err := scanChatSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func scanChatSession(row rowScanner) (model.ChatSession, error) {
	var (
		s         model.ChatSession
		ragJSON   string
		createdAt string
		updatedAt string
	)
	err := row.Scan(&s.ID, &s.Title, &s.SystemPrompt, &ragJSON, &createdAt, &updatedAt)
	if err != nil {
		return model.ChatSession{}, err
	}
	json.Unmarshal([]byte(ragJSON), &s.RAGRetrieval)
	s.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	s.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return s, nil
}

// ── ChatMessageRepo ───────────────────────────────────────────────────────────

// ChatMessageRepo implements port.ChatMessageRepo against SQLite.
type ChatMessageRepo struct{ db *sql.DB }

var _ port.ChatMessageRepo = (*ChatMessageRepo)(nil)

func (r *ChatMessageRepo) Append(ctx context.Context, sessionID, role, content string, sources []model.SourceRef) (model.ChatMessage, error) {
	id := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var sourcesJSON *string
	if sources != nil {
		b, _ := json.Marshal(sources)
		s := string(b)
		sourcesJSON = &s
	}
	_, err := r.db.ExecContext(ctx, q["chat.MessageAppend"],
		id, sessionID, role, content, sourcesJSON, now)
	if err != nil {
		return model.ChatMessage{}, err
	}
	t, _ := time.Parse(time.RFC3339Nano, now)
	return model.ChatMessage{
		ID:        id,
		SessionID: sessionID,
		Role:      role,
		Content:   content,
		Sources:   sources,
		CreatedAt: t,
	}, nil
}

func (r *ChatMessageRepo) List(ctx context.Context, sessionID string) ([]model.ChatMessage, error) {
	rows, err := r.db.QueryContext(ctx, q["chat.MessageList"], sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []model.ChatMessage
	for rows.Next() {
		var (
			m          model.ChatMessage
			sourcesStr *string
			createdAt  string
		)
		if err := rows.Scan(&m.ID, &m.ExternalID, &m.SessionID, &m.Role, &m.Content, &sourcesStr, &createdAt); err != nil {
			return nil, err
		}
		if sourcesStr != nil {
			json.Unmarshal([]byte(*sourcesStr), &m.Sources)
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}
