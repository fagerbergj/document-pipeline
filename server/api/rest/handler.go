package rest

import (
	"io/fs"
	"net/http"
	"os"

	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"

	"github.com/fagerbergj/document-pipeline/server/core"
	adktools "github.com/fagerbergj/document-pipeline/server/core/adk/tools"
	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// handler holds all dependencies needed by the HTTP handlers.
type handler struct {
	docs       port.DocumentRepo
	jobs       port.JobRepo
	artifacts  port.ArtifactRepo
	contexts   port.ContextRepo
	sessionSvc session.Service
	store      port.DocumentArtifactStore
	streams    port.StreamManager
	llm        port.LLMInference
	embed      port.EmbedStore
	search     port.DocumentIndexer
	ingest     *core.IngestService
	pipeline   model.PipelineConfig
	vaultPath  string
	embedModel string
	ragTool    tool.Tool
}

// Dependencies bundles the wiring passed to New.
type Dependencies struct {
	Documents  port.DocumentRepo
	Jobs       port.JobRepo
	Artifacts  port.ArtifactRepo
	Contexts   port.ContextRepo
	SessionSvc session.Service
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
	em := resolveEmbedModel(deps.Pipeline)
	ragTool, _ := adktools.NewRagSearchTool(deps.Embed, deps.LLM.GenerateEmbed, em)
	h := &handler{
		docs:       deps.Documents,
		jobs:       deps.Jobs,
		artifacts:  deps.Artifacts,
		contexts:   deps.Contexts,
		sessionSvc: deps.SessionSvc,
		store:      deps.Store,
		streams:    deps.Streams,
		llm:        deps.LLM,
		embed:      deps.Embed,
		search:     deps.Search,
		ingest:     deps.Ingest,
		pipeline:   deps.Pipeline,
		vaultPath:  deps.VaultPath,
		embedModel: em,
		ragTool:    ragTool,
	}
	return NewRouter(h, deps.FrontendFS)
}
