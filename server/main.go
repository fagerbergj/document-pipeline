package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/adk/session"
	"google.golang.org/adk/session/database"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/fagerbergj/document-pipeline/server/api/rest"
	"github.com/fagerbergj/document-pipeline/server/core"
	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/fagerbergj/document-pipeline/server/store/config"
	storeembed "github.com/fagerbergj/document-pipeline/server/store/embed"
	"github.com/fagerbergj/document-pipeline/server/store/filesystem"
	"github.com/fagerbergj/document-pipeline/server/store/ollama"
	storeopensearch "github.com/fagerbergj/document-pipeline/server/store/opensearch"
	"github.com/fagerbergj/document-pipeline/server/store/openwebui"
	"github.com/fagerbergj/document-pipeline/server/store/postgres"
	"github.com/fagerbergj/document-pipeline/server/store/prompts"
	"github.com/fagerbergj/document-pipeline/server/store/qdrant"
	"github.com/fagerbergj/document-pipeline/server/store/stream"
	"github.com/fagerbergj/document-pipeline/server/store/whisper"
	"github.com/fagerbergj/document-pipeline/server/web"
	"golang.org/x/sync/errgroup"
)

func main() {
	dsn := flag.String("db", envOr("DATABASE_URL", ""), "PostgreSQL DSN")
	migrationsDir := flag.String("migrations", envOr("MIGRATIONS_DIR", "db/migrations"), "Path to SQL migration files")
	vault := flag.String("vault", envOr("VAULT_PATH", "/data/vault"), "Artifact vault path")
	pipelineCfg := flag.String("pipeline", envOr("PIPELINE_CONFIG", "config/pipeline.yaml"), "Pipeline YAML config path")
	addr := flag.String("addr", envOr("LISTEN_ADDR", ":8000"), "HTTP listen address")
	ollamaURL := flag.String("ollama", envOr("OLLAMA_URL", "http://localhost:11434"), "Ollama base URL")
	whisperURL := flag.String("whisper", envOr("WHISPER_URL", "http://faster-whisper:8000"), "Whisper base URL")
	qdrantURL := flag.String("qdrant", envOr("QDRANT_URL", ""), "Qdrant base URL (empty = skip)")
	qdrantCollection := flag.String("qdrant-collection", envOr("QDRANT_COLLECTION", "documents"), "Qdrant collection name")
	qdrantKey := flag.String("qdrant-key", envOr("QDRANT_API_KEY", ""), "Qdrant API key")
	webUIURL := flag.String("webui", envOr("OPEN_WEBUI_URL", ""), "Open WebUI base URL (empty = skip)")
	webUIKey := flag.String("webui-key", envOr("OPEN_WEBUI_API_KEY", ""), "Open WebUI API key")
	webUIKnowledge := flag.String("webui-knowledge", envOr("OPEN_WEBUI_KNOWLEDGE_ID", ""), "Open WebUI knowledge base ID")
	opensearchURL := flag.String("opensearch", envOr("OPENSEARCH_URL", ""), "OpenSearch base URL (empty = skip)")
	opensearchIndex := flag.String("opensearch-index", envOr("OPENSEARCH_INDEX", "documents"), "OpenSearch index name")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// --- database ---
	if *dsn == "" {
		log.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	db, err := postgres.Open(*dsn, *migrationsDir)
	if err != nil {
		log.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	log.Info("database ready")

	// --- ADK session service ---
	// Shares the same Postgres DSN; GORM auto-migrates its four tables
	// (sessions, events, app_states, user_states) outside our migration system.
	sessionSvc, err := newSessionService(*dsn)
	if err != nil {
		log.Error("failed to create ADK session service", "err", err)
		os.Exit(1)
	}
	log.Info("ADK session service ready")

	// --- pipeline config ---
	pipeline, err := (&config.YAMLPipelineSource{Path: *pipelineCfg}).Load()
	if err != nil {
		log.Error("failed to load pipeline config", "err", err)
		os.Exit(1)
	}
	log.Info("pipeline loaded", "stages", len(pipeline.Stages))

	// --- adapters ---
	llm := ollama.New(*ollamaURL)
	transcriber := whisper.New(*whisperURL)
	fs := filesystem.New()
	sm := stream.New()
	renderer := &prompts.FilePromptRenderer{}

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

	var searchStore port.DocumentIndexer
	var indexerSvc *core.IndexerService
	if *opensearchURL != "" {
		osc := storeopensearch.NewClient(*opensearchURL, *opensearchIndex)
		if err := osc.EnsureIndex(context.Background()); err != nil {
			log.Warn("opensearch EnsureIndex failed — search disabled", "err", err)
		} else {
			searchStore = osc
			log.Info("opensearch ready", "index", *opensearchIndex)
		}
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
	worker := core.NewWorkerService(docs, jobs, artifacts, events, contexts, kv, fs, llm, embedStore, transcriber, sm, renderer, sessionSvc, pipeline, *vault)
	if searchStore != nil {
		indexerSvc = core.NewIndexerService(db.DB(), docs, jobs, artifacts, fs, searchStore, *vault)
	}

	handler := rest.New(rest.Dependencies{
		Documents:  docs,
		Jobs:       jobs,
		Artifacts:  artifacts,
		Contexts:   contexts,
		SessionSvc: sessionSvc,
		Store:      fs,
		Streams:    sm,
		LLM:        llm,
		Embed:      embedStore,
		Search:     searchStore,
		Ingest:     ingest,
		Pipeline:   pipeline,
		VaultPath:  *vault,
		FrontendFS: web.FS(),
	})
	srv := &http.Server{Addr: *addr, Handler: handler}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		log.Info("worker started")
		return worker.Run(egCtx)
	})

	if indexerSvc != nil {
		eg.Go(func() error {
			indexerSvc.Run(egCtx)
			return nil
		})
	}

	janitor := core.NewJanitorService(*vault, jobs, artifacts, fs)
	eg.Go(func() error {
		janitor.Run(egCtx)
		return nil
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

// newSessionService creates an ADK session.Service backed by Postgres.
// GORM auto-migrates its four tables on startup.
func newSessionService(dsn string) (session.Service, error) {
	svc, err := database.NewSessionService(gormpostgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}
	return svc, database.AutoMigrate(svc)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
