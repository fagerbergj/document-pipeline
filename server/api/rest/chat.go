package rest

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"

	"github.com/fagerbergj/document-pipeline/server/api/schema"
	"github.com/fagerbergj/document-pipeline/server/core"
	"github.com/fagerbergj/document-pipeline/server/core/adk"
	adktools "github.com/fagerbergj/document-pipeline/server/core/adk/tools"
	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const (
	stateKeyTitle         = "title"
	stateKeySystemPrompt  = "system_prompt"
	stateKeyCreatedAt     = "created_at"
	stateKeyRAGEnabled    = "rag_enabled"
	stateKeyRAGMaxSources = "rag_max_sources"
	stateKeyRAGMinScore   = "rag_min_score"
)

// defaultRAG applies when a chat is created without an explicit rag_retrieval
// body. Mirrors the frontend's New Chat defaults so API-created chats behave
// like UI-created ones. MinimumScore=0 means "no filter" in rag_search; 0.5
// drops the 0.49–0.55 noise band that pollutes top-k for short proper-noun
// queries against nomic-embed-text.
var defaultRAG = model.RAGConfig{
	Enabled:      true,
	MaxSources:   5,
	MinimumScore: 0.5,
}

// ── session state helpers ─────────────────────────────────────────────────────

func stateStr(sess session.Session, key string) string {
	v, err := sess.State().Get(key)
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

func stateFloat(sess session.Session, key string) float64 {
	v, err := sess.State().Get(key)
	if err != nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}

func stateBool(sess session.Session, key string) bool {
	v, err := sess.State().Get(key)
	if err != nil {
		return false
	}
	b, _ := v.(bool)
	return b
}

func stateTime(sess session.Session, key string) time.Time {
	s := stateStr(sess, key)
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func ragConfigFromSession(sess session.Session) model.RAGConfig {
	return model.RAGConfig{
		Enabled:      stateBool(sess, stateKeyRAGEnabled),
		MaxSources:   int(stateFloat(sess, stateKeyRAGMaxSources)),
		MinimumScore: stateFloat(sess, stateKeyRAGMinScore),
	}
}

func ragConfigToStateDelta(rag model.RAGConfig) map[string]any {
	return map[string]any{
		stateKeyRAGEnabled:    rag.Enabled,
		stateKeyRAGMaxSources: rag.MaxSources,
		stateKeyRAGMinScore:   rag.MinimumScore,
	}
}

// ── CRUD ──────────────────────────────────────────────────────────────────────

func (h *handler) listChats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pageSize := 20
	if ps := q.Get("page_size"); ps != "" {
		if n, err := strconv.Atoi(ps); err == nil && n >= 1 && n <= 100 {
			pageSize = n
		}
	}
	beforeID := q.Get("before_id")

	resp, err := h.sessionSvc.List(r.Context(), &session.ListRequest{
		AppName: adk.AppName,
		UserID:  adk.UserID,
	})
	if err != nil {
		slog.Error("listChats", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	sessions := resp.Sessions
	sort.Slice(sessions, func(i, j int) bool {
		return stateTime(sessions[i], stateKeyCreatedAt).After(stateTime(sessions[j], stateKeyCreatedAt))
	})

	if beforeID != "" {
		for i, s := range sessions {
			if s.ID() == beforeID {
				sessions = sessions[i+1:]
				break
			}
		}
	}

	var nextPageToken *string
	if len(sessions) > pageSize {
		last := sessions[pageSize-1].ID()
		nextPageToken = &last
		sessions = sessions[:pageSize]
	}

	data := make([]schema.ChatSummary, 0, len(sessions))
	for _, s := range sessions {
		data = append(data, toChatSummaryFromSession(s))
	}
	writeJSON(w, http.StatusOK, schema.PaginatedChats{
		Data:          data,
		NextPageToken: nextPageToken,
	})
}

func (h *handler) createChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SystemPrompt *string          `json:"system_prompt"`
		RAGRetrieval *model.RAGConfig `json:"rag_retrieval"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	rag := defaultRAG
	if body.RAGRetrieval != nil {
		rag = *body.RAGRetrieval
	}
	systemPrompt := ""
	if body.SystemPrompt != nil {
		systemPrompt = *body.SystemPrompt
	}

	chatID := uuid.NewString()
	now := time.Now().UTC()

	state := map[string]any{
		stateKeyTitle:        "",
		stateKeySystemPrompt: systemPrompt,
		stateKeyCreatedAt:    now.Format(time.RFC3339Nano),
	}
	for k, v := range ragConfigToStateDelta(rag) {
		state[k] = v
	}

	resp, err := h.sessionSvc.Create(r.Context(), &session.CreateRequest{
		AppName:   adk.AppName,
		UserID:    adk.UserID,
		SessionID: chatID,
		State:     state,
	})
	if err != nil {
		slog.Error("createChat", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, toChatSummaryFromSession(resp.Session))
}

func (h *handler) getChat(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "chat_id")
	resp, err := h.sessionSvc.Get(r.Context(), &session.GetRequest{
		AppName:   adk.AppName,
		UserID:    adk.UserID,
		SessionID: id,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	msgs := messagesFromSession(resp.Session)
	writeJSON(w, http.StatusOK, toChatDetailFromSession(resp.Session, msgs))
}

func (h *handler) patchChat(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "chat_id")
	resp, err := h.sessionSvc.Get(r.Context(), &session.GetRequest{
		AppName:   adk.AppName,
		UserID:    adk.UserID,
		SessionID: id,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	sess := resp.Session

	var body struct {
		Title        *string          `json:"title"`
		SystemPrompt *string          `json:"system_prompt"`
		RAGRetrieval *model.RAGConfig `json:"rag_retrieval"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	delta := map[string]any{}
	if body.Title != nil {
		delta[stateKeyTitle] = *body.Title
	}
	if body.SystemPrompt != nil {
		delta[stateKeySystemPrompt] = *body.SystemPrompt
	}
	if body.RAGRetrieval != nil {
		for k, v := range ragConfigToStateDelta(*body.RAGRetrieval) {
			delta[k] = v
		}
	}

	if len(delta) > 0 {
		if err := adk.AppendStateEvent(r.Context(), h.sessionSvc, id, delta); err != nil {
			slog.Error("patchChat AppendStateEvent", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		// Re-fetch to reflect updated state.
		resp2, err := h.sessionSvc.Get(r.Context(), &session.GetRequest{
			AppName:   adk.AppName,
			UserID:    adk.UserID,
			SessionID: id,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		sess = resp2.Session
	}
	writeJSON(w, http.StatusOK, toChatSummaryFromSession(sess))
}

func (h *handler) deleteChat(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "chat_id")
	err := h.sessionSvc.Delete(r.Context(), &session.DeleteRequest{
		AppName:   adk.AppName,
		UserID:    adk.UserID,
		SessionID: id,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── send message ──────────────────────────────────────────────────────────────

func (h *handler) sendChatMessage(w http.ResponseWriter, r *http.Request) {
	chatID := chi.URLParam(r, "chat_id")
	sessResp, err := h.sessionSvc.Get(r.Context(), &session.GetRequest{
		AppName:   adk.AppName,
		UserID:    adk.UserID,
		SessionID: chatID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	sess := sessResp.Session
	systemPrompt := stateStr(sess, stateKeySystemPrompt)
	existingTitle := stateStr(sess, stateKeyTitle)

	var body struct {
		Content string `json:"content"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	content := strings.TrimSpace(body.Content)
	if content == "" {
		writeError(w, http.StatusUnprocessableEntity, "content is required")
		return
	}

	rag := ragConfigFromSession(sess)
	ragTool, err := adktools.NewRagSearchTool(h.embed, h.llm.GenerateEmbed, h.embedModel, rag.MaxSources, rag.MinimumScore)
	if err != nil {
		slog.Error("sendChatMessage NewRagSearchTool", "chat_id", chatID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	collectStageData := func(ctx context.Context, docID string) (map[string]map[string]any, error) {
		return core.CollectStageData(ctx, h.jobs, h.artifacts, h.store, h.vaultPath, docID)
	}
	getDocsBatch := func(ctx context.Context, ids []string) (map[string]model.Document, error) {
		res, err := h.docs.ListPaginated(ctx, port.DocumentFilter{IDs: ids}, model.PageRequest{PageSize: len(ids)})
		if err != nil {
			return nil, err
		}
		out := make(map[string]model.Document, len(res.Data))
		for _, d := range res.Data {
			out[d.ID] = d
		}
		return out, nil
	}
	stageDataBatch := func(ctx context.Context, ids []string) (map[string]map[string]map[string]any, error) {
		return core.CollectStageDataBatch(ctx, h.jobs, h.artifacts, h.store, h.vaultPath, ids)
	}
	searchDocsTool, err := adktools.NewSearchDocumentsTool(h.search, getDocsBatch, stageDataBatch, 10)
	if err != nil {
		slog.Error("sendChatMessage NewSearchDocumentsTool", "chat_id", chatID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	getDocTool, err := adktools.NewGetDocumentTool(h.docs.Get, collectStageData)
	if err != nil {
		slog.Error("sendChatMessage NewGetDocumentTool", "chat_id", chatID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	sseHeaders(w)
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	queryModel := h.queryModel()

	instruction := "You are a helpful assistant with access to a personal notes knowledge base. " +
		"Use the retrieval tools (rag_search, search_documents, get_document) to find " +
		"relevant notes before answering — each tool's description tells you when to pick it.\n\n" +
		"Search multiple times with different queries — start broad, follow up on names and " +
		"terms you encounter. Only stop when results stop adding new information. " +
		"If a search returns no results, try one broader variant; if still empty, tell the user " +
		"you couldn't find anything rather than spamming more searches."
	if systemPrompt != "" {
		instruction += "\n\nAdditional context:\n" + systemPrompt
	}

	mdl := adk.NewPortLLMModel(h.llm, queryModel)
	userParts := []*genai.Part{{Text: content}}

	// Serialize all writes to the SSE stream so keepalives don't interleave with tokens.
	var writeMu sync.Mutex

	keepaliveCtx, stopKeepalive := context.WithCancel(r.Context())
	defer stopKeepalive()
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-keepaliveCtx.Done():
				return
			case <-t.C:
				writeMu.Lock()
				writeSSEComment(w, "keepalive")
				flusher.Flush()
				writeMu.Unlock()
			}
		}
	}()

	result, runErr := adk.RunAgent(r.Context(), mdl, []tool.Tool{ragTool, searchDocsTool, getDocTool}, instruction, userParts, h.sessionSvc, chatID, func(token string) {
		b, _ := json.Marshal(map[string]string{port.EventFieldText: token})
		writeMu.Lock()
		writeSSEEvent(w, port.EventToken, string(b))
		flusher.Flush()
		writeMu.Unlock()
	})
	stopKeepalive()
	writeMu.Lock()
	defer writeMu.Unlock()
	if runErr != nil {
		slog.Error("sendChatMessage RunAgent", "chat_id", chatID, "err", runErr)
		b, _ := json.Marshal(map[string]string{port.EventFieldError: runErr.Error()})
		writeSSEEvent(w, port.EventError, string(b))
		flusher.Flush()
		return
	}
	slog.Info("sendChatMessage agent done", "chat_id", chatID, "tool_responses", len(result.ToolResponses), "final_text_len", len(result.Text))

	// Auto-set title from first message.
	if existingTitle == "" {
		title := content
		if len(title) > 60 {
			title = title[:60]
		}
		_ = adk.AppendStateEvent(r.Context(), h.sessionSvc, chatID, map[string]any{stateKeyTitle: strings.TrimSpace(title)})
	}

	retrievedChunks := adktools.RagSourcesFromPayloads(result.ToolResponses)
	sources := make([]model.SourceRef, 0, len(retrievedChunks))
	for _, c := range retrievedChunks {
		sources = append(sources, model.SourceRef{
			Title:     c.Title,
			Text:      c.Text,
			DateMonth: c.DateMonth,
			Score:     c.Score,
		})
	}

	sourceBytes, _ := json.Marshal(toSourceDocs(sources))
	writeSSEEvent(w, "sources", string(sourceBytes))
	flusher.Flush()

	writeSSEEvent(w, port.EventDone, "{}")
	flusher.Flush()
}

func (h *handler) queryModel() string {
	if m := os.Getenv("QUERY_MODEL"); m != "" {
		return m
	}
	if m := os.Getenv("CLARIFY_MODEL"); m != "" {
		return m
	}
	return "qwen3:4b"
}

// ── session → schema converters ───────────────────────────────────────────────

func toChatSummaryFromSession(sess session.Session) schema.ChatSummary {
	title := stateStr(sess, stateKeyTitle)
	sysprompt := stateStr(sess, stateKeySystemPrompt)
	return schema.ChatSummary{
		Id:           sess.ID(),
		Title:        strPtr(title),
		SystemPrompt: strPtr(sysprompt),
		RagRetrieval: toRagRetrieval(ragConfigFromSession(sess)),
		CreatedAt:    stateTime(sess, stateKeyCreatedAt),
		UpdatedAt:    sess.LastUpdateTime(),
	}
}

func toChatDetailFromSession(sess session.Session, msgs []schema.ChatMessage) schema.ChatDetail {
	title := stateStr(sess, stateKeyTitle)
	sysprompt := stateStr(sess, stateKeySystemPrompt)
	return schema.ChatDetail{
		Id:           sess.ID(),
		Title:        strPtr(title),
		SystemPrompt: strPtr(sysprompt),
		RagRetrieval: toRagRetrieval(ragConfigFromSession(sess)),
		Messages:     &msgs,
		CreatedAt:    stateTime(sess, stateKeyCreatedAt),
		UpdatedAt:    sess.LastUpdateTime(),
	}
}

// ── message reconstruction from ADK session events ────────────────────────────

type chatTurn struct {
	invocationID   string
	userContent    string
	userEventID    string
	userTimestamp  time.Time
	modelContent   string
	modelEventID   string
	modelTimestamp time.Time
	toolResponses  []map[string]any
}

func messagesFromSession(sess session.Session) []schema.ChatMessage {
	turns := map[string]*chatTurn{}
	var order []string

	for e := range sess.Events().All() {
		if e.InvocationID == "" || e.Content == nil {
			continue
		}
		id := e.InvocationID
		if _, exists := turns[id]; !exists {
			turns[id] = &chatTurn{invocationID: id}
			order = append(order, id)
		}
		t := turns[id]

		for _, p := range e.Content.Parts {
			if p.FunctionResponse != nil && p.FunctionResponse.Response != nil {
				t.toolResponses = append(t.toolResponses, p.FunctionResponse.Response)
			}
		}
		if e.Content.Role == "user" && t.userContent == "" {
			for _, p := range e.Content.Parts {
				if p.Text != "" {
					t.userContent = p.Text
					t.userEventID = e.ID
					t.userTimestamp = e.Timestamp
					break
				}
			}
		}
		if e.IsFinalResponse() && e.Content.Role == "model" {
			for _, p := range e.Content.Parts {
				if p.Text != "" {
					t.modelContent = p.Text
					t.modelEventID = e.ID
					t.modelTimestamp = e.Timestamp
					break
				}
			}
		}
	}

	var msgs []schema.ChatMessage
	for _, id := range order {
		t := turns[id]
		if t.userContent != "" {
			emptyDocs := []schema.SourceDoc{}
			msgs = append(msgs, schema.ChatMessage{
				Id:        toUUID(t.userEventID),
				Role:      schema.User,
				Content:   t.userContent,
				Sources:   &emptyDocs,
				CreatedAt: t.userTimestamp,
			})
		}
		if t.modelContent != "" {
			chunks := adktools.RagSourcesFromPayloads(t.toolResponses)
			sources := make([]model.SourceRef, 0, len(chunks))
			for _, c := range chunks {
				sources = append(sources, model.SourceRef{
					Title:     c.Title,
					Text:      c.Text,
					DateMonth: c.DateMonth,
					Score:     c.Score,
				})
			}
			sdocs := toSourceDocs(sources)
			msgs = append(msgs, schema.ChatMessage{
				Id:        toUUID(t.modelEventID),
				Role:      schema.Assistant,
				Content:   t.modelContent,
				Sources:   &sdocs,
				CreatedAt: t.modelTimestamp,
			})
		}
	}
	return msgs
}
