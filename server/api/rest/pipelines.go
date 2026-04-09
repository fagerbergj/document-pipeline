package rest

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (h *handler) listPipelines(w http.ResponseWriter, r *http.Request) {
	stages := make([]map[string]any, 0, len(h.pipeline.Stages))
	for _, s := range h.pipeline.Stages {
		stages = append(stages, map[string]any{
			"name":  s.Name,
			"type":  s.Type,
			"model": nilIfEmpty(s.Model),
		})
	}
	pipeline := map[string]any{
		"id":     "pipeline",
		"name":   "pipeline",
		"stages": stages,
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":            []any{pipeline},
		"next_page_token": nil,
	})
}

func (h *handler) getPipeline(w http.ResponseWriter, r *http.Request) {
	if chi.URLParam(r, "pipeline_id") != "pipeline" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	stages := make([]map[string]any, 0, len(h.pipeline.Stages))
	for _, s := range h.pipeline.Stages {
		var inputs []string
		if s.Input != "" {
			inputs = []string{s.Input}
		}

		var outputs []map[string]any
		for _, o := range s.Outputs {
			outputs = append(outputs, map[string]any{"field": o.Field, "type": o.Type})
		}
		if outputs == nil && s.Output != "" {
			outputs = []map[string]any{{"field": s.Output, "type": "text"}}
		}

		stage := map[string]any{
			"name":        s.Name,
			"type":        s.Type,
			"model":       nilIfEmpty(s.Model),
			"inputs":      nilIfEmpty(inputs),
			"outputs":     nilIfEmpty(outputs),
			"skip_if":     s.SkipIf,
			"start_if":    s.StartIf,
			"continue_if": s.ContinueIf,
		}
		stages = append(stages, stage)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":     "pipeline",
		"name":   "pipeline",
		"stages": stages,
	})
}

// nilIfEmpty returns nil if v is the zero value for its type, v otherwise.
// Handles string, []string, []map[string]any.
func nilIfEmpty(v any) any {
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
	case []string:
		if len(t) == 0 {
			return nil
		}
	case []map[string]any:
		if len(t) == 0 {
			return nil
		}
	}
	return v
}
