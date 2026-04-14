package rest

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/go-chi/chi/v5"
)

var defaultRAG = model.RAGConfig{
	Enabled:      true,
	MaxSources:   5,
	MinimumScore: 0.0,
}

func (h *handler) listChats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pageSize := 20
	if ps := q.Get("page_size"); ps != "" {
		if n, err := strconv.Atoi(ps); err == nil && n >= 1 && n <= 100 {
			pageSize = n
		}
	}
	var beforeID *string
	if bid := q.Get("before_id"); bid != "" {
		beforeID = &bid
	}

	chats, err := h.chats.List(r.Context(), pageSize, beforeID)
	if err != nil {
		slog.Error("listChats", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	data := make([]any, 0, len(chats))
	for _, c := range chats {
		data = append(data, chatSummaryJSON(c))
	}
	var nextBeforeID any
	if len(chats) == pageSize {
		nextBeforeID = chats[len(chats)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":           data,
		"next_before_id": nextBeforeID,
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
	chat, err := h.chats.Create(r.Context(), systemPrompt, rag)
	if err != nil {
		slog.Error("createChat", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, chatSummaryJSON(chat))
}

func (h *handler) getChat(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "chat_id")
	chat, found, err := h.chats.Get(r.Context(), id)
	if err != nil || !found {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	msgs, err := h.messages.List(r.Context(), id)
	if err != nil {
		slog.Error("getChat messages", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	detail := chatSummaryJSON(chat)
	msgJSON := make([]any, 0, len(msgs))
	for _, m := range msgs {
		msgJSON = append(msgJSON, chatMessageJSON(m))
	}
	detail["messages"] = msgJSON
	writeJSON(w, http.StatusOK, detail)
}

func (h *handler) patchChat(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "chat_id")
	if _, found, err := h.chats.Get(r.Context(), id); err != nil || !found {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	var body struct {
		Title        *string          `json:"title"`
		SystemPrompt *string          `json:"system_prompt"`
		RAGRetrieval *model.RAGConfig `json:"rag_retrieval"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	updates := port.ChatSessionUpdates{
		Title:        body.Title,
		SystemPrompt: body.SystemPrompt,
		RAGRetrieval: body.RAGRetrieval,
	}
	chat, err := h.chats.Update(r.Context(), id, updates)
	if err != nil {
		slog.Error("patchChat", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, chatSummaryJSON(chat))
}

func (h *handler) deleteChat(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "chat_id")
	deleted, err := h.chats.Delete(r.Context(), id)
	if err != nil || !deleted {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) sendChatMessage(w http.ResponseWriter, r *http.Request) {
	chatID := chi.URLParam(r, "chat_id")
	chat, found, err := h.chats.Get(r.Context(), chatID)
	if err != nil || !found {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

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

	history, err := h.messages.List(r.Context(), chatID)
	if err != nil {
		slog.Error("sendChatMessage list history", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if _, err := h.messages.Append(r.Context(), chatID, "user", content, nil); err != nil {
		slog.Error("sendChatMessage append user", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Auto-set title from first message
	if chat.Title == "" {
		title := content
		if len(title) > 60 {
			title = title[:60]
		}
		title = strings.TrimSpace(title)
		_ = h.updateChatTitle(r.Context(), chatID, title)
	}

	sseHeaders(w)
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Resolve embed model and query model from pipeline config
	embedModel := os.Getenv("EMBED_MODEL")
	if embedModel == "" {
		embedModel = "nomic-embed-text:v1.5"
	}
	for _, s := range h.pipeline.Stages {
		if s.Type == model.StageTypeEmbed && s.Model != "" {
			embedModel = s.Model
			break
		}
	}
	queryModel := os.Getenv("QUERY_MODEL")
	if queryModel == "" {
		queryModel = os.Getenv("CLARIFY_MODEL")
	}
	if queryModel == "" {
		queryModel = "qwen3:4b"
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// RAG retrieval
	rag := chat.RAGRetrieval
	var sources []model.SourceRef
	if rag.Enabled && rag.MaxSources > 0 {
		queryVec, err := h.llm.GenerateEmbed(ctx, embedModel, content)
		if err != nil {
			b, _ := json.Marshal(map[string]string{port.EventFieldError: err.Error()})
			writeSSEEvent(w, port.EventError, string(b))
			flusher.Flush()
			return
		}
		results, err := h.embed.Search(ctx, queryVec, rag.MaxSources)
		if err != nil {
			slog.Warn("sendChatMessage embed search", "err", err)
		}
		for _, res := range results {
			if rag.MinimumScore > 0 && res.Score < rag.MinimumScore {
				continue
			}
			sources = append(sources, model.SourceRef{
				DocumentID: stringPayload(res.Payload, port.PayloadDocID),
				Title:      stringPayload(res.Payload, port.PayloadTitle),
				Summary:    stringPayload(res.Payload, port.PayloadSummary),
				DateMonth:  stringPayload(res.Payload, port.PayloadDateMonth),
				Score:      res.Score,
			})
		}
	}

	// Send sources event
	sourceBytes, _ := json.Marshal(sources)
	writeSSEEvent(w, "sources", string(sourceBytes))
	flusher.Flush()

	// Build system prompt with RAG context
	var notesBlock strings.Builder
	for _, res := range sources {
		notesBlock.WriteString("---\n")
		notesBlock.WriteString("Title: " + res.Title)
		if res.DateMonth != "" {
			notesBlock.WriteString(" (" + res.DateMonth + ")")
		}
		notesBlock.WriteString("\n" + res.Summary + "\n\n")
	}
	systemPrompt := chat.SystemPrompt
	ctxBlock := ""
	if systemPrompt != "" {
		ctxBlock = "\nAdditional context:\n" + systemPrompt + "\n"
	}
	notesSection := "\n(No matching notes found.)\n"
	if notesBlock.Len() > 0 {
		notesSection = "\nRetrieved notes:\n" + notesBlock.String()
	}
	systemContent := "You are a helpful assistant with access to a personal notes knowledge base. " +
		"Answer based on the retrieved notes. If they don't contain enough information, say so." +
		ctxBlock + notesSection

	// Build message history
	llmMessages := []port.LLMMessage{{Role: "system", Content: systemContent}}
	for _, m := range history {
		if m.Role == "user" || m.Role == "assistant" {
			llmMessages = append(llmMessages, port.LLMMessage{Role: m.Role, Content: m.Content})
		}
	}
	llmMessages = append(llmMessages, port.LLMMessage{Role: "user", Content: content})

	// Stream LLM response
	var buf strings.Builder
	streamErr := h.llm.ChatStream(ctx, queryModel, llmMessages, func(token string) {
		buf.WriteString(token)
		b, _ := json.Marshal(map[string]string{port.EventFieldText: token})
		writeSSEEvent(w, port.EventToken, string(b))
		flusher.Flush()
	})

	if streamErr != nil {
		b, _ := json.Marshal(map[string]string{port.EventFieldError: streamErr.Error()})
		writeSSEEvent(w, port.EventError, string(b))
		flusher.Flush()
		return
	}

	writeSSEEvent(w, port.EventDone, "{}")
	flusher.Flush()

	// Persist assistant message
	if buf.Len() > 0 {
		if _, err := h.messages.Append(r.Context(), chatID, "assistant", buf.String(), sources); err != nil {
			slog.Error("sendChatMessage append assistant", "err", err)
		}
	}
}

func (h *handler) updateChatTitle(ctx context.Context, chatID, title string) error {
	_, err := h.chats.Update(ctx, chatID, port.ChatSessionUpdates{Title: &title})
	return err
}

// ── JSON helpers ──────────────────────────────────────────────────────────────

func chatSummaryJSON(c model.ChatSession) map[string]any {
	var title any
	if c.Title != "" {
		title = c.Title
	}
	return map[string]any{
		"id":            c.ID,
		"title":         title,
		"system_prompt": c.SystemPrompt,
		"rag_retrieval": c.RAGRetrieval,
		"created_at":    c.CreatedAt,
		"updated_at":    c.UpdatedAt,
	}
}

func chatMessageJSON(m model.ChatMessage) map[string]any {
	sources := m.Sources
	if sources == nil {
		sources = []model.SourceRef{}
	}
	return map[string]any{
		"id":          m.ID,
		"external_id": m.ExternalID,
		"role":        m.Role,
		"content":     m.Content,
		"sources":     sources,
		"created_at":  m.CreatedAt,
	}
}

func stringPayload(payload map[string]any, key string) string {
	if v, ok := payload[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
