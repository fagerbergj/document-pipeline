package rest

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/fagerbergj/document-pipeline/server/api/schema"
	"github.com/fagerbergj/document-pipeline/server/core"
	"github.com/fagerbergj/document-pipeline/server/core/model"
)

func (h *handler) receiveWebhook(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid multipart form")
		return
	}

	var meta map[string]any
	if dataStr := r.FormValue("data"); dataStr != "" {
		_ = json.Unmarshal([]byte(dataStr), &meta)
	}
	if meta == nil {
		meta = map[string]any{}
	}

	file, header, err := r.FormFile("attachment")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "attachment field required")
		return
	}
	defer file.Close()

	imageBytes, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read attachment")
		return
	}

	attachmentFilename := header.Filename
	title := webhookTitle(meta, attachmentFilename)

	req := core.IngestRequest{
		FileBytes: imageBytes,
		Filename:  attachmentFilename,
		FileType:  model.FileTypePNG,
		Title:     title,
		Meta: core.IngestMeta{
			Meta:               meta,
			AttachmentFilename: attachmentFilename,
		},
	}

	_, _, err = h.ingest.Ingest(r.Context(), req)
	if err != nil {
		slog.Error("receiveWebhook ingest", "err", err)
		writeError(w, http.StatusInternalServerError, "ingest failed")
		return
	}

	// Always return 200 even on duplicate — rmfakecloud retries on non-200 responses.
	writeJSON(w, http.StatusOK, schema.OkResponse{Ok: true})
}

// webhookTitle derives a document title from rmfakecloud metadata.
// Destination notebook names take priority; attachment filename is the fallback.
func webhookTitle(meta map[string]any, attachmentFilename string) string {
	if dests, ok := meta["destinations"].([]any); ok {
		for _, d := range dests {
			if name := strings.TrimSpace(fmt.Sprint(d)); name != "" {
				return name
			}
		}
	}
	return ""
}
