package rest

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/fagerbergj/document-pipeline/server/core"
	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (h *handler) listJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sort := q.Get("sort")
	if sort == "" {
		sort = "pipeline"
	}
	pageSize := 50
	if ps := q.Get("page_size"); ps != "" {
		if n, err := strconv.Atoi(ps); err == nil && n >= 1 && n <= 1000 {
			pageSize = n
		}
	}

	filter := port.JobFilter{Sort: sort}
	if v := q.Get("job_id"); v != "" {
		filter.IDs = splitCSV(v)
	}
	if v := q.Get("document_id"); v != "" {
		filter.DocumentIDs = splitCSV(v)
	}
	if v := q.Get("stages"); v != "" {
		filter.Stages = splitCSV(v)
	}
	if v := q.Get("statuses"); v != "" {
		filter.Statuses = splitCSV(v)
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

	result, err := h.jobs.ListPaginated(r.Context(), filter, pageReq)
	if err != nil {
		slog.Error("listJobs", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Batch-fetch document titles
	docCache := map[string]*string{}
	for _, job := range result.Data {
		if _, seen := docCache[job.DocumentID]; !seen {
			doc, err := h.docs.Get(r.Context(), job.DocumentID)
			if err == nil {
				docCache[job.DocumentID] = doc.Title
			} else {
				docCache[job.DocumentID] = nil
			}
		}
	}

	data := make([]any, 0, len(result.Data))
	for _, job := range result.Data {
		data = append(data, jobSummary(job, docCache[job.DocumentID]))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data":            data,
		"next_page_token": result.NextPageToken,
	})
}

func (h *handler) getJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "job_id")
	job, err := h.jobs.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	doc, _ := h.docs.Get(r.Context(), job.DocumentID)
	writeJSON(w, http.StatusOK, jobDetail(job, titleOf(doc)))
}

func (h *handler) patchJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "job_id")
	job, err := h.jobs.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	var body struct {
		Options *model.JobOptions `json:"options"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	if body.Options != nil {
		now := time.Now().UTC()
		// Merge: only update non-zero fields
		merged := job.Options
		if body.Options.RequireContext {
			merged.RequireContext = body.Options.RequireContext
		}
		if body.Options.Embed != nil {
			merged.Embed = body.Options.Embed
		}
		if err := h.jobs.UpdateOptions(r.Context(), id, merged, now); err != nil {
			slog.Error("patchJob UpdateOptions", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	job, err = h.jobs.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	doc, _ := h.docs.Get(r.Context(), job.DocumentID)
	writeJSON(w, http.StatusOK, jobDetail(job, titleOf(doc)))
}

func (h *handler) patchRun(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "job_id")
	runID := chi.URLParam(r, "run_id")

	job, err := h.jobs.GetByID(r.Context(), jobID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	var body struct {
		Questions   []model.Question   `json:"questions"`
		Suggestions *model.Suggestions `json:"suggestions"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	// Find and update the run
	runIdx := -1
	for i, run := range job.Runs {
		if run.ID == runID {
			runIdx = i
			break
		}
	}
	if runIdx < 0 {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	now := time.Now().UTC()
	if body.Questions != nil {
		job.Runs[runIdx].Questions = body.Questions
	}
	if body.Suggestions != nil {
		job.Runs[runIdx].Suggestions = *body.Suggestions
	}
	job.Runs[runIdx].UpdatedAt = now

	if err := h.jobs.UpdateRuns(r.Context(), jobID, job.Runs, now); err != nil {
		slog.Error("patchRun UpdateRuns", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, runJSON(job.Runs[runIdx]))
}

var validTransitions = map[model.JobStatus]map[model.JobStatus]bool{
	model.JobStatusRunning: {model.JobStatusError: true},
	model.JobStatusWaiting: {model.JobStatusPending: true, model.JobStatusDone: true},
	model.JobStatusError:   {model.JobStatusPending: true},
	model.JobStatusDone:    {model.JobStatusPending: true},
}

func (h *handler) putJobStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "job_id")
	job, err := h.jobs.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	var body struct {
		Status model.JobStatus `json:"status"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	target := body.Status
	if !validTransitions[job.Status][target] {
		writeError(w, http.StatusUnprocessableEntity,
			"invalid transition: "+string(job.Status)+" → "+string(target))
		return
	}

	now := time.Now().UTC()
	if err := h.jobs.UpdateStatus(r.Context(), id, string(target), now); err != nil {
		slog.Error("putJobStatus UpdateStatus", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if target == model.JobStatusDone {
		// Advance pipeline: upsert next stage job to pending
		if err := h.advancePipeline(r, job, now); err != nil {
			slog.Error("putJobStatus advancePipeline", "err", err)
		}
	}

	if target == model.JobStatusPending && job.Status == model.JobStatusDone {
		stageOrder := make([]string, len(h.pipeline.Stages))
		for i, s := range h.pipeline.Stages {
			stageOrder[i] = s.Name
		}
		if err := h.jobs.CascadeReplay(r.Context(), job.DocumentID, job.Stage, stageOrder, now); err != nil {
			slog.Error("putJobStatus CascadeReplay", "err", err)
		}
	}

	job, err = h.jobs.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	doc, _ := h.docs.Get(r.Context(), job.DocumentID)
	writeJSON(w, http.StatusOK, jobDetail(job, titleOf(doc)))
}

func (h *handler) streamJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "job_id")
	job, err := h.jobs.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	initialStatus := job.Status

	sseHeaders(w)
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	ch := h.streams.Subscribe(id)
	defer h.streams.Unsubscribe(id)

	lastStatusCheck := time.Now()
	for {
		select {
		case <-r.Context().Done():
			return
		case evt, ok := <-ch:
			if !ok {
				writeSSEEvent(w, port.EventDone, "{}")
				flusher.Flush()
				return
			}
			if evt.Type == port.EventDone {
				writeSSEEvent(w, port.EventDone, "{}")
				flusher.Flush()
				return
			}
			data := evt.Data
			if evt.Type == port.EventToken {
				b, _ := json.Marshal(map[string]string{"text": data})
				data = string(b)
			}
			writeSSEEvent(w, evt.Type, data)
			flusher.Flush()
		case <-time.After(1500 * time.Millisecond):
			// Keepalive ping
			writeSSEComment(w, "ping")
			flusher.Flush()
			// Periodic status check — send done if status changed
			if time.Since(lastStatusCheck) > 3*time.Second {
				lastStatusCheck = time.Now()
				current, err := h.jobs.GetByID(r.Context(), id)
				if err == nil && current.Status != initialStatus {
					writeSSEEvent(w, port.EventDone, "{}")
					flusher.Flush()
					return
				}
			}
		}
	}
}

// advancePipeline upserts the next stage job to pending after a job completes.
func (h *handler) advancePipeline(r *http.Request, job model.Job, now time.Time) error {
	stageNames := make([]string, len(h.pipeline.Stages))
	for i, s := range h.pipeline.Stages {
		stageNames[i] = s.Name
	}
	idx := -1
	for i, name := range stageNames {
		if name == job.Stage {
			idx = i
			break
		}
	}
	if idx < 0 || idx+1 >= len(stageNames) {
		return nil
	}
	nextStage := stageNames[idx+1]
	existing, found, err := h.jobs.GetByDocumentAndStage(r.Context(), job.DocumentID, nextStage)
	if err != nil {
		return err
	}
	if found {
		return h.jobs.UpdateStatus(r.Context(), existing.ID, string(model.JobStatusPending), now)
	}
	nextJob := model.Job{
		ID:         uuid.NewString(),
		DocumentID: job.DocumentID,
		Stage:      nextStage,
		Status:     model.JobStatusPending,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	return h.jobs.Upsert(r.Context(), nextJob)
}

// ── response helpers ──────────────────────────────────────────────────────────

func jobSummary(job model.Job, title *string) map[string]any {
	return map[string]any{
		"id":          job.ID,
		"document_id": job.DocumentID,
		"title":       title,
		"stage":       job.Stage,
		"status":      job.Status,
		"created_at":  job.CreatedAt,
		"updated_at":  job.UpdatedAt,
	}
}

func jobDetail(job model.Job, title *string) map[string]any {
	runs := make([]any, 0, len(job.Runs))
	for _, run := range job.Runs {
		runs = append(runs, runJSON(run))
	}
	m := jobSummary(job, title)
	m["options"] = job.Options
	m["runs"] = runs
	return m
}

func runJSON(run model.Run) map[string]any {
	return map[string]any{
		"id":          run.ID,
		"inputs":      run.Inputs,
		"outputs":     run.Outputs,
		"confidence":  run.Confidence,
		"questions":   run.Questions,
		"suggestions": run.Suggestions,
		"created_at":  run.CreatedAt,
		"updated_at":  run.UpdatedAt,
	}
}
