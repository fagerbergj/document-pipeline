package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/fagerbergj/document-pipeline/server/api/rest"
	"github.com/fagerbergj/document-pipeline/server/core"
	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/fagerbergj/document-pipeline/server/store/config"
	storeembed "github.com/fagerbergj/document-pipeline/server/store/embed"
	"github.com/fagerbergj/document-pipeline/server/store/filesystem"
	"github.com/fagerbergj/document-pipeline/server/store/ollama"
	"github.com/fagerbergj/document-pipeline/server/store/openwebui"
	"github.com/fagerbergj/document-pipeline/server/store/prompts"
	"github.com/fagerbergj/document-pipeline/server/store/qdrant"
	"github.com/fagerbergj/document-pipeline/server/store/sqlite"
	"github.com/fagerbergj/document-pipeline/server/store/stream"
	"github.com/fagerbergj/document-pipeline/server/web"
	"golang.org/x/sync/errgroup"
)

func main() {
	dbPath := flag.String("db", envOr("DB_PATH", "/data/pipeline.db"), "SQLite database path")
	migrationsDir := flag.String("migrations", envOr("MIGRATIONS_DIR", "db/migrations"), "Path to SQL migration files")
	vault := flag.String("vault", envOr("VAULT_PATH", "/data/vault"), "Artifact vault path")
	pipelineCfg := flag.String("pipeline", envOr("PIPELINE_CONFIG", "config/pipeline.yaml"), "Pipeline YAML config path")
	addr := flag.String("addr", envOr("LISTEN_ADDR", ":8000"), "HTTP listen address")
	ollamaURL := flag.String("ollama", envOr("OLLAMA_URL", "http://localhost:11434"), "Ollama base URL")
	qdrantURL := flag.String("qdrant", envOr("QDRANT_URL", ""), "Qdrant base URL (empty = skip)")
	qdrantCollection := flag.String("qdrant-collection", envOr("QDRANT_COLLECTION", "documents"), "Qdrant collection name")
	qdrantKey := flag.String("qdrant-key", envOr("QDRANT_API_KEY", ""), "Qdrant API key")
	webUIURL := flag.String("webui", envOr("OPEN_WEBUI_URL", ""), "Open WebUI base URL (empty = skip)")
	webUIKey := flag.String("webui-key", envOr("OPEN_WEBUI_API_KEY", ""), "Open WebUI API key")
	webUIKnowledge := flag.String("webui-knowledge", envOr("OPEN_WEBUI_KNOWLEDGE_ID", ""), "Open WebUI knowledge base ID")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// --- database ---
	db, err := sqlite.Open(*dbPath, *migrationsDir)
	if err != nil {
		log.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	log.Info("database ready", "path", *dbPath)

	// --- pipeline config ---
	pipeline, err := (&config.YAMLPipelineSource{Path: *pipelineCfg}).Load()
	if err != nil {
		log.Error("failed to load pipeline config", "err", err)
		os.Exit(1)
	}
	log.Info("pipeline loaded", "stages", len(pipeline.Stages))

	// --- adapters ---
	llm := ollama.New(*ollamaURL)
	fs := filesystem.New()
	sm := stream.New()
	renderer := &prompts.FilePromptRenderer{}

	// Build embed store: requires Qdrant; Open WebUI is optional.
	var embedStore port.EmbedStore
	if *qdrantURL != "" {
		q := qdrant.New(*qdrantURL, *qdrantCollection, *qdrantKey)
		if *webUIURL != "" && *webUIKey != "" && *webUIKnowledge != "" {
			w := openwebui.New(*webUIURL, *webUIKey, *webUIKnowledge)
			embedStore = storeembed.New(q, w)
			log.Info("embed store: Qdrant + Open WebUI")
		} else {
			embedStore = storeembed.NewQdrantOnly(q)
			log.Info("embed store: Qdrant only")
		}
	} else {
		embedStore = storeembed.NewNoop()
		log.Warn("embed store: disabled (no --qdrant URL)")
	}

	// --- repositories ---
	docs := db.Documents()
	jobs := db.Jobs()
	artifacts := db.Artifacts()
	events := db.StageEvents()
	contexts := db.Contexts()
	kv := db.KeyValues()

	// --- services ---
	ingest := core.NewIngestService(docs, jobs, artifacts, events, kv, fs, pipeline, *vault)
	worker := core.NewWorkerService(docs, jobs, artifacts, events, contexts, kv, fs, llm, embedStore, sm, renderer, pipeline, *vault)

	// --- HTTP server ---
	handler := rest.New(rest.Dependencies{
		Documents:  docs,
		Jobs:       jobs,
		Artifacts:  artifacts,
		Contexts:   contexts,
		Chats:      db.Chats(),
		Messages:   db.ChatMessages(),
		Store:      fs,
		Streams:    sm,
		LLM:        llm,
		Embed:      embedStore,
		Ingest:     ingest,
		Pipeline:   pipeline,
		VaultPath:  *vault,
		FrontendFS: web.FS(),
	})
	srv := &http.Server{Addr: *addr, Handler: handler}

	// --- run until signal ---
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		log.Info("worker started")
		return worker.Run(egCtx)
	})

	eg.Go(func() error {
		log.Info("HTTP server starting", "addr", *addr)
		go func() {
			<-egCtx.Done()
			srv.Close()
		}()
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	})

	if err := eg.Wait(); err != nil {
		log.Error("server error", "err", err)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
