package rest

import (
	"net/http"

	"github.com/fagerbergj/document-pipeline/server/api/schema"
	"github.com/go-chi/chi/v5"
)

func (h *handler) listPipelines(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, schema.PaginatedPipelines{
		Data: []schema.Pipeline{toPipeline(h.pipeline)},
	})
}

func (h *handler) getPipeline(w http.ResponseWriter, r *http.Request) {
	if chi.URLParam(r, "pipeline_id") != "pipeline" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, toPipelineDetail(h.pipeline))
}
