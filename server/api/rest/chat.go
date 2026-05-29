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

// getUserIDForSession returns the UserID to use for ADK session operations.
// Every /chats route is protected by requireAuth, so an authenticated user is always present.
// If no auth user is found (which would indicate a bug in the middleware chain), this panics.
func getUserIDForSession(r *http.Request) string {
	user, ok := AuthUserFromContext(r.Context())
	if !ok {
		panic("getUserIDForSession called without authenticated user")
	}
	return user
}

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
	userID := getUserIDForSession(r)

	resp, err := h.sessionSvc.List(r.Context(), &session.ListRequest{
		AppName: adk.AppName,
		UserID:  userID,
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
		UserID:    getUserIDForSession(r),
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
		UserID:    getUserIDForSession(r),
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
		UserID:    getUserIDForSession(r),
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
			UserID:    getUserIDForSession(r),
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
		UserID:    getUserIDForSession(r),
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
		UserID:    getUserIDForSession(r),
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
	tools, err := h.buildChatTools(rag)
	if err != nil {
		slog.Error("sendChatMessage buildChatTools", "chat_id", chatID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	mdl := adk.NewPortLLMModel(h.llm, h.queryModel())
	userParts := []*genai.Part{{Text: content}}
	result, ok := h.streamAgentRun(w, r, chatID, mdl, tools, chatInstruction(systemPrompt), userParts, "sendChatMessage")
	if !ok {
		return
	}
	slog.Info("sendChatMessage agent done", "chat_id", chatID, "tool_responses", len(result.ToolResponses), "final_text_len", len(result.Text))

	if existingTitle == "" {
		title := content
		if len(title) > 60 {
			title = title[:60]
		}
		_ = adk.AppendStateEvent(r.Context(), h.sessionSvc, chatID, map[string]any{stateKeyTitle: strings.TrimSpace(title)})
	}
}

// streamAgentRun is the shared SSE-streaming body used by every chat
// endpoint that drives the agent loop (initial message + confirmation
// resume). It sets the SSE headers, runs a keepalive ticker on a side
// goroutine, calls adk.RunAgent forwarding each stream event as an SSE
// frame, and writes the terminating done/error event.
//
// Returns (result, true) on success, (zero, false) if the stream could not
// be set up or RunAgent returned an error — callers should treat false as
// "stop, response already written" and skip post-stream work.
//
// logTag is just a slog prefix so error logs distinguish caller sites.
func (h *handler) streamAgentRun(
	w http.ResponseWriter,
	r *http.Request,
	chatID string,
	mdl *adk.PortLLMModel,
	tools []tool.Tool,
	instruction string,
	userParts []*genai.Part,
	logTag string,
) (adk.RunResult, bool) {
	sseHeaders(w)
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return adk.RunResult{}, false
	}

	// Serialize all writes to the SSE stream so keepalives don't interleave
	// with token / tool / confirmation events from the agent loop.
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

	result, runErr := adk.RunAgent(r.Context(), mdl, tools, instruction, userParts, h.sessionSvc, chatID, func(ev adk.StreamEvent) {
		eventType := ev.SSEEventType()
		if eventType == "" {
			return
		}
		payload, err := ev.JSONPayload()
		if err != nil {
			return
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		writeSSEEvent(w, eventType, string(payload))
		flusher.Flush()
	})
	stopKeepalive()
	writeMu.Lock()
	defer writeMu.Unlock()
	if runErr != nil {
		slog.Error(logTag+" RunAgent", "chat_id", chatID, "err", runErr)
		b, _ := json.Marshal(map[string]string{port.EventFieldError: runErr.Error()})
		writeSSEEvent(w, port.EventError, string(b))
		flusher.Flush()
		return adk.RunResult{}, false
	}
	writeSSEEvent(w, port.EventDone, "{}")
	flusher.Flush()
	return result, true
}

// buildUpdateDocumentTool constructs the update_document ADK tool wired to the
// handler's repos. Both sendChatMessage (initial turn) and confirmChatToolCall
// (resume after user decision) need this tool registered with the same
// closures so ADK can re-invoke the same tool body on resume.
func (h *handler) buildUpdateDocumentTool() (tool.Tool, error) {
	deps := core.StageUpdateDeps{
		Jobs:       h.jobs,
		Artifacts:  h.artifacts,
		Store:      h.store,
		SessionSvc: h.sessionSvc,
		Pipeline:   h.pipeline,
		VaultPath:  h.vaultPath,
	}
	read := func(ctx context.Context, docID string) (string, string, string, error) {
		return core.CurrentCanonicalBody(ctx, deps, docID)
	}
	// The stage+field are resolved at request time and pinned via the
	// confirmation payload, so the apply writes the exact field the user
	// approved even if the canonical body shifted in the meantime.
	update := func(ctx context.Context, docID, stage, field, content string) ([]string, error) {
		return core.UpdateStageArtifactAt(ctx, deps, docID, stage, field, content)
	}
	return adktools.NewUpdateDocumentTool(read, update)
}

// confirmChatToolCall handles a user's approve/reject decision on a pending
// tool-call confirmation. On approve, persists the user's FunctionResponse
// into the session and resumes the agent loop; the SSE response streams the
// continuation. On reject, persists the rejection but does NOT resume the
// agent — the turn ends with a single `done` event.
func (h *handler) confirmChatToolCall(w http.ResponseWriter, r *http.Request) {
	chatID := chi.URLParam(r, "chat_id")
	callID := chi.URLParam(r, "call_id")

	sessResp, err := h.sessionSvc.Get(r.Context(), &session.GetRequest{
		AppName:   adk.AppName,
		UserID:    getUserIDForSession(r),
		SessionID: chatID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	sess := sessResp.Session
	systemPrompt := stateStr(sess, stateKeySystemPrompt)

	var body struct {
		Confirmed bool    `json:"confirmed"`
		Content   *string `json:"content"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	// Persist the decision so RequestConfirmationRequestProcessor can pair it
	// with the original tool call on the next runner.Run call.
	//
	// ADK rebuilds the resumed ToolConfirmation purely from this response and
	// discards the original request payload, so any request-time context the
	// tool needs at apply time must be echoed back here. For update_document
	// that's the resolved stage+field the user actually reviewed: re-attaching
	// them lets the apply write that exact field instead of re-resolving the
	// canonical body fresh (which could have shifted between request and approval).
	payload := map[string]any{}
	if body.Content != nil {
		payload["content"] = *body.Content
	}
	if reqPayload, ok := adk.RequestedConfirmationPayload(sess, callID); ok {
		if stage, ok := reqPayload["stage"].(string); ok && stage != "" {
			payload["stage"] = stage
		}
		if field, ok := reqPayload["field"].(string); ok && field != "" {
			payload["field"] = field
		}
	}
	if len(payload) == 0 {
		payload = nil
	}
	if err := adk.AppendConfirmationResponse(r.Context(), h.sessionSvc, chatID, callID, body.Confirmed, payload); err != nil {
		slog.Error("confirmChatToolCall AppendConfirmationResponse", "chat_id", chatID, "call_id", callID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Reject short-circuit: the rejection is persisted in the session and
	// will be observed naturally on the next user message; we do not resume
	// the agent loop here. Stream a single done event for UI symmetry.
	if !body.Confirmed {
		sseHeaders(w)
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "streaming not supported")
			return
		}
		writeSSEEvent(w, port.EventDone, "{}")
		flusher.Flush()
		slog.Info("confirmChatToolCall rejected", "chat_id", chatID, "call_id", callID)
		return
	}

	// Approve: rebuild the same toolset and resume the runner with no new
	// user message. ADK pairs the persisted FunctionResponse with the
	// original tool call and re-invokes the tool with Confirmed=true.
	rag := ragConfigFromSession(sess)
	tools, err := h.buildChatTools(rag)
	if err != nil {
		slog.Error("confirmChatToolCall buildChatTools", "chat_id", chatID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	mdl := adk.NewPortLLMModel(h.llm, h.queryModel())
	_, _ = h.streamAgentRun(w, r, chatID, mdl, tools, chatInstruction(systemPrompt), nil, "confirmChatToolCall")
}

// buildChatTools constructs the full toolset used by the chat agent. Shared
// between the initial-message handler and the confirmation-resume handler so
// the resumed runner sees the same tools the original turn was built with —
// ADK requires the original tool to still be registered for the confirmation
// processor to find it.
func (h *handler) buildChatTools(rag model.RAGConfig) ([]tool.Tool, error) {
	ragTool, err := adktools.NewRagSearchTool(h.embed, h.llm.GenerateEmbed, h.embedModel, rag.MaxSources, rag.MinimumScore)
	if err != nil {
		return nil, err
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
		return nil, err
	}
	getDocTool, err := adktools.NewGetDocumentTool(h.docs.Get, collectStageData)
	if err != nil {
		return nil, err
	}
	updateDocTool, err := h.buildUpdateDocumentTool()
	if err != nil {
		return nil, err
	}
	return []tool.Tool{ragTool, searchDocsTool, getDocTool, updateDocTool}, nil
}

// chatInstruction returns the system instruction for the chat agent. Single
// source of truth shared by sendChatMessage and confirmChatToolCall.
func chatInstruction(systemPrompt string) string {
	instruction := "You are a helpful assistant with access to a personal notes knowledge base. " +
		"Use the retrieval tools (rag_search, search_documents, get_document) to find " +
		"relevant notes before answering — each tool's description tells you when to pick it.\n\n" +
		"Search multiple times with different queries — start broad, follow up on names and " +
		"terms you encounter. Only stop when results stop adding new information. " +
		"If a search returns no results, try one broader variant; if still empty, tell the user " +
		"you couldn't find anything rather than spamming more searches.\n\n" +
		"When answering, rely on the canonical body (full_text) — that is the polished version the " +
		"user reads. The retrieval tools return only that by default.\n\n" +
		"Whenever the user asks to fix, correct, rewrite, or change what a note says, call " +
		"update_document with the document id and the FULL corrected body as `content` — read the " +
		"current text with get_document first so you preserve everything they didn't ask to change. " +
		"Do not just describe the change in prose: update_document is what edits the note, and it " +
		"always shows the user an approval card (with a before/after diff) before anything is " +
		"written, so call it rather than asking the user for permission yourself. The user can edit " +
		"or reject your proposal there; on approval the summary, tags, and embeddings re-derive from " +
		"the new body automatically.\n\n" +
		"After an edit is applied, read the new value back with get_document (Postgres-backed, " +
		"immediately fresh). Do not rely on rag_search to confirm a just-applied edit — it is " +
		"eventually consistent and may return the old text until the embed stage re-runs."
	if systemPrompt != "" {
		instruction += "\n\nAdditional context:\n" + systemPrompt
	}
	return instruction
}

func (h *handler) queryModel() string {
	if m := os.Getenv("CHAT_MODEL"); m != "" {
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
				if p.Text != "" && !p.Thought {
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
			msgs = append(msgs, schema.ChatMessage{
				Id:        toUUID(t.userEventID),
				Role:      schema.User,
				Content:   t.userContent,
				CreatedAt: t.userTimestamp,
			})
		}
		if t.modelContent != "" {
			msgs = append(msgs, schema.ChatMessage{
				Id:        toUUID(t.modelEventID),
				Role:      schema.Assistant,
				Content:   t.modelContent,
				CreatedAt: t.modelTimestamp,
			})
		}
	}
	return msgs
}
