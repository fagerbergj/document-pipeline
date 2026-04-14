package rest

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/go-chi/chi/v5"
)

func (h *handler) listContexts(w http.ResponseWriter, r *http.Request) {
	entries, err := h.contexts.List(r.Context())
	if err != nil {
		slog.Error("listContexts", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	data := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		data = append(data, contextJSON(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":            data,
		"next_page_token": nil,
	})
}

func (h *handler) createContext(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Text string `json:"text"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	text := strings.TrimSpace(body.Text)
	if name == "" || text == "" {
		writeError(w, http.StatusUnprocessableEntity, "name and text are required")
		return
	}
	entry, err := h.contexts.Create(r.Context(), name, text)
	if err != nil {
		slog.Error("createContext", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, contextJSON(entry))
}

func (h *handler) updateContext(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "context_id")
	var body struct {
		Name *string `json:"name"`
		Text *string `json:"text"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Name != nil {
		s := strings.TrimSpace(*body.Name)
		body.Name = &s
	}
	if body.Text != nil {
		s := strings.TrimSpace(*body.Text)
		body.Text = &s
	}
	entry, err := h.contexts.Update(r.Context(), id, body.Name, body.Text)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, contextJSON(entry))
}

func (h *handler) deleteContext(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "context_id")
	deleted, err := h.contexts.Delete(r.Context(), id)
	if err != nil || !deleted {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func contextJSON(e model.Context) map[string]any {
	return map[string]any{
		"id":   e.ID,
		"name": e.Name,
		"text": e.Text,
	}
}
