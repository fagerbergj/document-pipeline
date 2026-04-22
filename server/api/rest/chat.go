package rest

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/genai"

	"github.com/fagerbergj/document-pipeline/server/api/schema"
	"github.com/fagerbergj/document-pipeline/server/core/adk"
	adktools "github.com/fagerbergj/document-pipeline/server/core/adk/tools"
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

	data := make([]schema.ChatSummary, 0, len(chats))
	for _, c := range chats {
		data = append(data, toChatSummary(c))
	}
	var nextPageToken *string
	if len(chats) == pageSize {
		last := chats[len(chats)-1].ID
		nextPageToken = &last
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
	chat, err := h.chats.Create(r.Context(), systemPrompt, rag)
	if err != nil {
		slog.Error("createChat", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, toChatSummary(chat))
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
	writeJSON(w, http.StatusOK, toChatDetail(chat, msgs))
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
	writeJSON(w, http.StatusOK, toChatSummary(chat))
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

	rawHistory, err := h.messages.List(r.Context(), chatID)
	if err != nil {
		slog.Error("sendChatMessage list history", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	history := make([]port.LLMMessage, 0, len(rawHistory))
	for _, m := range rawHistory {
		if m.Role == "user" || m.Role == "assistant" {
			history = append(history, port.LLMMessage{Role: m.Role, Content: m.Content})
		}
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

	queryModel := os.Getenv("QUERY_MODEL")
	if queryModel == "" {
		queryModel = os.Getenv("CLARIFY_MODEL")
	}
	if queryModel == "" {
		queryModel = "qwen3:4b"
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	instruction := "You are a helpful assistant with access to a personal notes knowledge base. " +
		"Use the rag_search tool to find relevant notes before answering. " +
		"If you cannot find relevant information, say so."
	if chat.SystemPrompt != "" {
		instruction += "\n\nAdditional context:\n" + chat.SystemPrompt
	}

	ragTool, err := adktools.NewRagSearchTool(h.embed, h.llm.GenerateEmbed, h.embedModel)
	if err != nil {
		b, _ := json.Marshal(map[string]string{port.EventFieldError: err.Error()})
		writeSSEEvent(w, port.EventError, string(b))
		flusher.Flush()
		return
	}

	mdl := adk.NewPortLLMModel(h.llm, queryModel)
	userParts := []*genai.Part{{Text: content}}
	result, runErr := adk.RunAgent(ctx, mdl, []tool.Tool{ragTool}, instruction, userParts, history)
	if runErr != nil {
		b, _ := json.Marshal(map[string]string{port.EventFieldError: runErr.Error()})
		writeSSEEvent(w, port.EventError, string(b))
		flusher.Flush()
		return
	}

	// Collect sources from tool call results.
	retrievedChunks := adktools.RagSourcesFromPayloads(result.ToolResponses)
	var sources []model.SourceRef
	for _, c := range retrievedChunks {
		sources = append(sources, model.SourceRef{
			Title:     c.Title,
			Text:      c.Text,
			DateMonth: c.DateMonth,
			Score:     c.Score,
		})
	}

	// Send sources event then the full response text.
	sourceBytes, _ := json.Marshal(sources)
	writeSSEEvent(w, "sources", string(sourceBytes))
	flusher.Flush()

	responseText := result.Text
	b, _ := json.Marshal(map[string]string{port.EventFieldText: responseText})
	writeSSEEvent(w, port.EventToken, string(b))
	flusher.Flush()

	writeSSEEvent(w, port.EventDone, "{}")
	flusher.Flush()

	// Persist assistant message.
	if responseText != "" {
		if _, err := h.messages.Append(r.Context(), chatID, "assistant", responseText, sources); err != nil {
			slog.Error("sendChatMessage append assistant", "err", err)
		}
	}
}

func (h *handler) updateChatTitle(ctx context.Context, chatID, title string) error {
	_, err := h.chats.Update(ctx, chatID, port.ChatSessionUpdates{Title: &title})
	return err
}
