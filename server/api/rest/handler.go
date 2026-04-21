package rest

import (
	"io/fs"
	"net/http"
	"os"

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
	search    port.DocumentIndexer
	ingest     *core.IngestService
	pipeline   model.PipelineConfig
	vaultPath  string
	embedModel string
}

// Dependencies bundles the wiring passed to New. Using a struct instead of a long
// positional parameter list prevents silent nil mis-wiring (previously main.go
// accidentally passed nil for FrontendFS, which made the SPA fallback a dead
// branch at runtime).
type Dependencies struct {
	Documents  port.DocumentRepo
	Jobs       port.JobRepo
	Artifacts  port.ArtifactRepo
	Contexts   port.ContextRepo
	Chats      port.ChatRepo
	Messages   port.ChatMessageRepo
	Store      port.DocumentArtifactStore
	Streams    port.StreamManager
	LLM        port.LLMInference
	Embed      port.EmbedStore
	Search     port.DocumentIndexer
	Ingest     *core.IngestService
	Pipeline   model.PipelineConfig
	VaultPath  string
	FrontendFS fs.FS
}

func resolveEmbedModel(pipeline model.PipelineConfig) string {
	for _, s := range pipeline.Stages {
		if s.Type == model.StageTypeEmbed && s.Model != "" {
			return s.Model
		}
	}
	if em := os.Getenv("EMBED_MODEL"); em != "" {
		return em
	}
	return "nomic-embed-text:v1.5"
}

// New constructs the HTTP handler and returns the fully wired router.
func New(deps Dependencies) http.Handler {
	h := &handler{
		docs:      deps.Documents,
		jobs:      deps.Jobs,
		artifacts: deps.Artifacts,
		contexts:  deps.Contexts,
		chats:     deps.Chats,
		messages:  deps.Messages,
		store:     deps.Store,
		streams:   deps.Streams,
		llm:       deps.LLM,
		embed:     deps.Embed,
		search:    deps.Search,
		ingest:     deps.Ingest,
		pipeline:   deps.Pipeline,
		vaultPath:  deps.VaultPath,
		embedModel: resolveEmbedModel(deps.Pipeline),
	}
	return NewRouter(h, deps.FrontendFS)
}
