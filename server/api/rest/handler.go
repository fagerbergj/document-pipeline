package rest

import (
	"io/fs"
	"net/http"

	"github.com/fagerbergj/document-pipeline/server/core"
	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// handler holds all dependencies needed by the HTTP handlers.
type handler struct {
	docs      port.DocumentRepo
	jobs      port.JobRepo
	artifacts port.ArtifactRepo
	contexts  port.ContextRepo
	chats     port.ChatRepo
	messages  port.ChatMessageRepo
	store     port.DocumentArtifactStore
	streams   port.StreamManager
	llm       port.LLMInference
	embed     port.EmbedStore
	ingest    *core.IngestService
	pipeline  model.PipelineConfig
	vaultPath string
}

// New constructs the HTTP handler and returns the fully wired router.
func New(
	docs port.DocumentRepo,
	jobs port.JobRepo,
	artifacts port.ArtifactRepo,
	contexts port.ContextRepo,
	chats port.ChatRepo,
	messages port.ChatMessageRepo,
	store port.DocumentArtifactStore,
	streams port.StreamManager,
	llm port.LLMInference,
	embed port.EmbedStore,
	ingest *core.IngestService,
	pipeline model.PipelineConfig,
	vaultPath string,
	frontendFS fs.FS,
) http.Handler {
	h := &handler{
		docs:      docs,
		jobs:      jobs,
		artifacts: artifacts,
		contexts:  contexts,
		chats:     chats,
		messages:  messages,
		store:     store,
		streams:   streams,
		llm:       llm,
		embed:     embed,
		ingest:    ingest,
		pipeline:  pipeline,
		vaultPath: vaultPath,
	}
	return NewRouter(h, frontendFS)
}
