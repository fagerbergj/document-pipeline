package rest

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fagerbergj/document-pipeline/server/api/schema"
	"github.com/fagerbergj/document-pipeline/server/core"
	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/go-chi/chi/v5"
)

var supportedFileTypes = map[string]model.FileType{
	"png":  model.FileTypePNG,
	"jpg":  model.FileTypeJPG,
	"jpeg": model.FileTypeJPEG,
	"txt":  model.FileTypeTXT,
	"md":   model.FileTypeMD,
}

func (h *handler) listDocuments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sort := q.Get("sort")
	if sort == "" {
		sort = "pipeline"
	}
	pageSize := 20
	if ps := q.Get("page_size"); ps != "" {
		if n, err := strconv.Atoi(ps); err == nil && n >= 1 && n <= 200 {
			pageSize = n
		}
	}

	filter := port.DocumentFilter{Sort: sort}
	if stages := q.Get("stages"); stages != "" {
		filter.Stages = splitCSV(stages)
	}
	if statuses := q.Get("statuses"); statuses != "" {
		filter.Statuses = splitCSV(statuses)
	}

	pageReq := model.PageRequest{PageSize: pageSize}
	if pt := q.Get("page_token"); pt != "" {
		tok, err := core.DecodePageToken(pt)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid page_token")
			return
		}
		pageReq.PageToken = &tok
	}

	result, err := h.docs.ListPaginated(r.Context(), filter, pageReq)
	if err != nil {
		slog.Error("listDocuments", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	data := make([]schema.DocumentSummary, 0, len(result.Data))
	for _, doc := range result.Data {
		jobs, err := h.jobs.ListForDocument(r.Context(), doc.ID)
		if err != nil {
			slog.Error("listDocuments jobs", "err", err)
		}
		data = append(data, toDocSummary(doc, pickCurrentJob(jobs)))
	}

	writeJSON(w, http.StatusOK, schema.PaginatedDocuments{
		Data:          data,
		NextPageToken: result.NextPageToken,
	})
}

func (h *handler) uploadDocument(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "file field required")
		return
	}
	defer file.Close()

	filename := header.Filename
	if filename == "" {
		filename = "upload"
	}

	ext := ""
	if idx := strings.LastIndex(filename, "."); idx >= 0 {
		ext = strings.ToLower(filename[idx+1:])
	}
	ft, ok := supportedFileTypes[ext]
	if !ok {
		writeError(w, http.StatusBadRequest, "unsupported file type '."+ext+"'")
		return
	}

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read file")
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	additionalContext := strings.TrimSpace(r.FormValue("additional_context"))

	var linkedContexts []string
	if lc := r.FormValue("linked_contexts"); lc != "" {
		if err := json.Unmarshal([]byte(lc), &linkedContexts); err != nil {
			linkedContexts = nil
		}
	}

	req := core.IngestRequest{
		FileBytes:         fileBytes,
		Filename:          filename,
		FileType:          ft,
		Title:             title,
		AdditionalContext: additionalContext,
		LinkedContexts:    linkedContexts,
	}

	job, ok, err := h.ingest.Ingest(r.Context(), req)
	if err != nil {
		slog.Error("uploadDocument ingest", "err", err)
		writeError(w, http.StatusInternalServerError, "ingest failed")
		return
	}
	if !ok {
		writeError(w, http.StatusConflict, "Duplicate file — document already exists")
		return
	}

	doc, _ := h.docs.Get(r.Context(), job.DocumentID)
	writeJSON(w, http.StatusCreated, toJobDetail(job, titleOf(doc)))
}

func (h *handler) getDocument(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "doc_id")
	doc, err := h.docs.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	detail, err := h.buildDocDetail(r, doc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *handler) patchDocument(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "doc_id")
	doc, err := h.docs.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	var body struct {
		Title             *string  `json:"title"`
		AdditionalContext *string  `json:"additional_context"`
		LinkedContexts    []string `json:"linked_contexts"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	now := time.Now().UTC()
	if body.Title != nil {
		s := strings.TrimSpace(*body.Title)
		if s == "" {
			doc.Title = nil
		} else {
			doc.Title = &s
		}
		doc.UpdatedAt = now
	}
	if body.AdditionalContext != nil {
		doc.AdditionalContext = strings.TrimSpace(*body.AdditionalContext)
		doc.UpdatedAt = now
	}
	if body.LinkedContexts != nil {
		doc.LinkedContexts = body.LinkedContexts
		doc.UpdatedAt = now
	}

	if err := h.docs.Update(r.Context(), doc); err != nil {
		slog.Error("patchDocument update", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	doc, err = h.docs.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	detail, err := h.buildDocDetail(r, doc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *handler) deleteDocument(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "doc_id")
	if _, err := h.docs.Get(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err := h.docs.Delete(r.Context(), id); err != nil {
		slog.Error("deleteDocument", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, schema.OkResponse{Ok: true})
}

func (h *handler) getArtifact(w http.ResponseWriter, r *http.Request) {
	docID := chi.URLParam(r, "doc_id")
	artifactID := chi.URLParam(r, "artifact_id")

	artifact, err := h.artifacts.Get(r.Context(), docID, artifactID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	data, err := h.store.Read(h.vaultPath, artifactID, artifact.Filename)
	if err != nil {
		writeError(w, http.StatusNotFound, "artifact file not found")
		return
	}

	w.Header().Set("Content-Type", artifact.ContentType)
	w.Header().Set("Content-Disposition", `inline; filename="`+filepath.Base(artifact.Filename)+`"`)
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (h *handler) buildDocDetail(r *http.Request, doc model.Document) (schema.DocumentDetail, error) {
	artifacts, err := h.artifacts.ListForDocument(r.Context(), doc.ID)
	if err != nil {
		slog.Error("buildDocDetail artifacts", "err", err)
		return schema.DocumentDetail{}, err
	}
	jobs, err := h.jobs.ListForDocument(r.Context(), doc.ID)
	if err != nil {
		slog.Error("buildDocDetail jobs", "err", err)
		return schema.DocumentDetail{}, err
	}
	return toDocDetail(doc, pickCurrentJob(jobs), artifacts), nil
}

// pickCurrentJob returns the most relevant job for display.
// Priority: running > waiting > pending > error > done (most recently updated).
func pickCurrentJob(jobs []model.Job) *model.Job {
	if len(jobs) == 0 {
		return nil
	}
	for _, status := range []model.JobStatus{
		model.JobStatusRunning,
		model.JobStatusWaiting,
		model.JobStatusPending,
		model.JobStatusError,
	} {
		for i := range jobs {
			if jobs[i].Status == status {
				return &jobs[i]
			}
		}
	}
	// All done — return most recently updated
	latest := &jobs[0]
	for i := range jobs[1:] {
		if jobs[i+1].UpdatedAt.After(latest.UpdatedAt) {
			latest = &jobs[i+1]
		}
	}
	return latest
}

func titleOf(doc model.Document) *string { return doc.Title }

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
