package rest

import (
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter builds and returns the Chi router with all routes registered.
// frontendFS should be the embedded frontend/dist directory; pass nil to skip
// static file serving (useful in tests).
func NewRouter(h *handler, frontendFS fs.FS) http.Handler {
	r := chi.NewRouter()
	r.Use(corsMiddleware)
	r.Use(loggingMiddleware)
	r.Use(middleware.Recoverer)

	r.Route("/api/v1", func(r chi.Router) {
		// Pipelines
		r.Get("/pipelines", h.listPipelines)
		r.Get("/pipelines/{pipeline_id}", h.getPipeline)

		// Documents
		r.Get("/documents", h.listDocuments)
		r.Post("/documents", h.uploadDocument)
		r.Get("/documents/{doc_id}", h.getDocument)
		r.Patch("/documents/{doc_id}", h.patchDocument)
		r.Delete("/documents/{doc_id}", h.deleteDocument)
		r.Get("/documents/{doc_id}/artifacts/{artifact_id}", h.getArtifact)

		// Jobs
		r.Get("/jobs", h.listJobs)
		r.Get("/jobs/{job_id}", h.getJob)
		r.Patch("/jobs/{job_id}", h.patchJob)
		r.Patch("/jobs/{job_id}/runs/{run_id}", h.patchRun)
		r.Put("/jobs/{job_id}/status", h.putJobStatus)
		r.Get("/jobs/{job_id}/stream", h.streamJob)

		// Contexts
		r.Get("/contexts", h.listContexts)
		r.Post("/contexts", h.createContext)
		r.Patch("/contexts/{context_id}", h.updateContext)
		r.Delete("/contexts/{context_id}", h.deleteContext)

		// Ingest
		r.Post("/remarkable/webhook", h.receiveWebhook)

		// Chat
		r.Get("/chats", h.listChats)
		r.Post("/chats", h.createChat)
		r.Get("/chats/{chat_id}", h.getChat)
		r.Patch("/chats/{chat_id}", h.patchChat)
		r.Delete("/chats/{chat_id}", h.deleteChat)
		r.Post("/chats/{chat_id}/messages", h.sendChatMessage)
	})

	// SPA fallback — serve frontend static files, fall through to index.html
	if frontendFS != nil {
		fileServer := http.FileServer(http.FS(frontendFS))
		r.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Try to serve the file; if not found, serve index.html
			_, err := frontendFS.Open(r.URL.Path)
			if err != nil {
				r2 := *r
				r2.URL.Path = "/"
				fileServer.ServeHTTP(w, &r2)
				return
			}
			fileServer.ServeHTTP(w, r)
		}))
	}

	return r
}
