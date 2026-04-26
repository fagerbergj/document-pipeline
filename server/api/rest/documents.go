package rest

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fagerbergj/document-pipeline/server/api/schema"
	"github.com/fagerbergj/document-pipeline/server/core"
	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

var supportedFileTypes = map[string]model.FileType{
	"png":  model.FileTypePNG,
	"jpg":  model.FileTypeJPG,
	"jpeg": model.FileTypeJPEG,
	"txt":  model.FileTypeTXT,
	"md":   model.FileTypeMD,
	"webm": model.FileTypeWEBM,
	"wav":  model.FileTypeWAV,
	"mp3":  model.FileTypeMP3,
	"m4a":  model.FileTypeM4A,
	"ogg":  model.FileTypeOGG,
	"flac": model.FileTypeFLAC,
}

func (h *handler) listDocuments(w http.ResponseWriter, r *http.Request) {
	qp := r.URL.Query()
	sort := qp.Get("sort")
	if sort == "" {
		sort = "pipeline"
	}
	pageSize := 20
	if ps := qp.Get("page_size"); ps != "" {
		if n, err := strconv.Atoi(ps); err == nil && n >= 1 && n <= 200 {
			pageSize = n
		}
	}

	filter := port.DocumentFilter{Sort: sort}

	var searchNextToken *string

	// Lucene search via OpenSearch — offset-based pagination encoded in page_token.
	luceneQuery := strings.TrimSpace(qp.Get("q"))
	if luceneQuery != "" && h.search != nil {
		from := 0
		if pt := qp.Get("page_token"); pt != "" {
			if offset, ok := core.DecodeOffsetToken(pt); ok {
				from = offset
			}
		}
		ids, total, err := h.search.Search(r.Context(), luceneQuery, from, pageSize)
		if err != nil {
			slog.Warn("listDocuments opensearch search", "err", err)
		} else if len(ids) == 0 {
			writeJSON(w, http.StatusOK, schema.PaginatedDocuments{Data: []schema.DocumentSummary{}})
			return
		} else {
			filter.IDs = ids
			if nextFrom := from + pageSize; nextFrom < total {
				tok := core.EncodeOffsetToken(nextFrom)
				searchNextToken = &tok
			}
		}
	}

	pageReq := model.PageRequest{PageSize: pageSize}
	if filter.IDs == nil {
		if pt := qp.Get("page_token"); pt != "" {
			tok, err := core.DecodePageToken(pt)
			if err != nil {
				writeError(w, http.StatusUnprocessableEntity, "invalid page_token")
				return
			}
			pageReq.PageToken = &tok
		}
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

	// Re-sort by OpenSearch relevance order when IDs came from search.
	if len(filter.IDs) > 0 {
		order := make(map[string]int, len(filter.IDs))
		for i, id := range filter.IDs {
			order[id] = i
		}
		for i := range data {
			if _, ok := order[data[i].Id.String()]; !ok {
				order[data[i].Id.String()] = len(filter.IDs) + i
			}
		}
		sortDocsByOrder(data, order)
	}

	nextToken := result.NextPageToken
	if searchNextToken != nil {
		nextToken = searchNextToken
	}
	writeJSON(w, http.StatusOK, schema.PaginatedDocuments{
		Data:          data,
		NextPageToken: nextToken,
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

	title := strings.TrimSpace(r.FormValue("title"))
	additionalContext := strings.TrimSpace(r.FormValue("additional_context"))
	series := strings.TrimSpace(r.FormValue("series"))

	var linkedContexts []string
	if lc := r.FormValue("linked_contexts"); lc != "" {
		if err := json.Unmarshal([]byte(lc), &linkedContexts); err != nil {
			linkedContexts = nil
		}
	}

	// Stream the multipart file to <vault>/tmp via io.Copy and route through
	// IngestStreamed so we never buffer the whole upload in memory.
	tmpDir := filepath.Join(h.vaultPath, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "tmp dir")
		return
	}
	tmpPath := filepath.Join(tmpDir, uuid.NewString()+".bin")
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create tmp file")
		return
	}
	if _, err := io.Copy(tmpFile, file); err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
		writeError(w, http.StatusBadRequest, "upload failed: "+err.Error())
		return
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		writeError(w, http.StatusInternalServerError, "close tmp file")
		return
	}

	req := core.IngestStreamedRequest{
		TempFilePath:      tmpPath,
		Filename:          filename,
		FileType:          ft,
		Title:             title,
		AdditionalContext: additionalContext,
		LinkedContexts:    linkedContexts,
		Series:            series,
		Meta: core.IngestMeta{
			AttachmentFilename: filename,
			FileType:           ft,
		},
	}

	job, ok, err := h.ingest.IngestStreamed(r.Context(), req)
	if err != nil {
		slog.Error("uploadDocument ingest", "err", err)
		_ = os.Remove(tmpPath)
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

func (h *handler) streamDocument(w http.ResponseWriter, r *http.Request) {
	qp := r.URL.Query()
	filename := strings.TrimSpace(qp.Get("filename"))
	if filename == "" {
		writeError(w, http.StatusBadRequest, "?filename= required")
		return
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

	tmpDir := filepath.Join(h.vaultPath, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "tmp dir")
		return
	}
	tmpPath := filepath.Join(tmpDir, uuid.NewString()+".bin")
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create tmp file")
		return
	}

	written, err := io.Copy(tmpFile, r.Body)
	tmpFile.Close()
	if err != nil {
		_ = os.Remove(tmpPath)
		writeError(w, http.StatusBadRequest, "upload aborted: "+err.Error())
		return
	}
	if written == 0 {
		_ = os.Remove(tmpPath)
		writeError(w, http.StatusBadRequest, "empty upload")
		return
	}

	req := core.IngestStreamedRequest{
		TempFilePath:      tmpPath,
		Filename:          filename,
		FileType:          ft,
		Title:             strings.TrimSpace(qp.Get("title")),
		AdditionalContext: strings.TrimSpace(qp.Get("additional_context")),
		Series:            strings.TrimSpace(qp.Get("series")),
		Meta: core.IngestMeta{
			AttachmentFilename: filename,
			FileType:           ft,
		},
	}
	job, ok, err := h.ingest.IngestStreamed(r.Context(), req)
	if err != nil {
		slog.Error("streamDocument ingest", "err", err)
		_ = os.Remove(tmpPath)
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
		Series            *string  `json:"series"`
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
	if body.Series != nil {
		s := strings.TrimSpace(*body.Series)
		if s == "" {
			doc.Series = nil
		} else {
			doc.Series = &s
		}
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
	if h.search != nil {
		_ = h.search.Delete(r.Context(), id)
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

	var data []byte
	if artifact.Path != nil && *artifact.Path != "" {
		data, err = h.store.ReadAt(h.vaultPath, *artifact.Path)
	} else {
		data, err = h.store.Read(h.vaultPath, artifactID, artifact.Filename)
	}
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

func pickCurrentJob(jobs []model.Job) *model.Job { return core.PickCurrentJob(jobs) }

func titleOf(doc model.Document) *string { return doc.Title }

func sortDocsByOrder(data []schema.DocumentSummary, order map[string]int) {
	for i := 1; i < len(data); i++ {
		for j := i; j > 0 && order[data[j].Id.String()] < order[data[j-1].Id.String()]; j-- {
			data[j], data[j-1] = data[j-1], data[j]
		}
	}
}

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
