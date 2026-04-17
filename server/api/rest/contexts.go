package rest

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/fagerbergj/document-pipeline/server/api/schema"
	"github.com/go-chi/chi/v5"
)

func (h *handler) listContexts(w http.ResponseWriter, r *http.Request) {
	entries, err := h.contexts.List(r.Context())
	if err != nil {
		slog.Error("listContexts", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	data := make([]schema.ContextEntry, 0, len(entries))
	for _, e := range entries {
		data = append(data, toContextEntry(e))
	}
	writeJSON(w, http.StatusOK, schema.PaginatedContexts{Data: data})
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
	writeJSON(w, http.StatusCreated, toContextEntry(entry))
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
	writeJSON(w, http.StatusOK, toContextEntry(entry))
}

func (h *handler) deleteContext(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "context_id")
	deleted, err := h.contexts.Delete(r.Context(), id)
	if err != nil || !deleted {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, schema.OkResponse{Ok: true})
}
