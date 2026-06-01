package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"google.golang.org/adk/session"
	"google.golang.org/adk/session/database"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/fagerbergj/document-pipeline/server/api/mcp"
	"github.com/fagerbergj/document-pipeline/server/api/rest"
	"github.com/fagerbergj/document-pipeline/server/core"
	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/fagerbergj/document-pipeline/server/store/config"
	storeembed "github.com/fagerbergj/document-pipeline/server/store/embed"
	"github.com/fagerbergj/document-pipeline/server/store/filesystem"
	"github.com/fagerbergj/document-pipeline/server/store/openai"
	storeopensearch "github.com/fagerbergj/document-pipeline/server/store/opensearch"
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
	llmURL := flag.String("llm-url", envOr("LLM_URL", "http://llm-swap:11436"), "OpenAI-compatible LLM base URL")
	whisperURL := flag.String("whisper", envOr("WHISPER_URL", "http://faster-whisper:8000"), "Whisper base URL")
	qdrantURL := flag.String("qdrant", envOr("QDRANT_URL", ""), "Qdrant base URL (empty = skip)")
	qdrantCollection := flag.String("qdrant-collection", envOr("QDRANT_COLLECTION", "documents"), "Qdrant collection name")
	qdrantKey := flag.String("qdrant-key", envOr("QDRANT_API_KEY", ""), "Qdrant API key")
	opensearchURL := flag.String("opensearch", envOr("OPENSEARCH_URL", ""), "OpenSearch base URL (empty = skip)")
	opensearchIndex := flag.String("opensearch-index", envOr("OPENSEARCH_INDEX", "documents"), "OpenSearch index name")
	benchMode := flag.Bool("bench", false, "Run an embedding-retrieval bench (queries as positional args) against the live RAG path, then exit")
	benchTopK := flag.Int("bench-topk", 5, "bench: number of hits to show per query")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Bench mode reuses the real embed + hybrid-search code (see bench.go) and
	// exits before the server's DB/session wiring — it only needs the pipeline
	// config, the LLM client, and Qdrant.
	if *benchMode {
		runBenchEmbed(log, *pipelineCfg, *llmURL, *qdrantURL, *qdrantCollection, *qdrantKey, *benchTopK, flag.Args())
		return
	}

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
	llm := openai.New(*llmURL, envOr("LLM_API_KEY", ""))
	transcriber := whisper.New(*whisperURL)
	fs := filesystem.New()
	sm := stream.New()
	renderer := &prompts.FilePromptRenderer{}

	var embedStore port.EmbedStore
	if *qdrantURL != "" {
		q := qdrant.New(*qdrantURL, *qdrantCollection, *qdrantKey)
		if fields := pipeline.QdrantPayloadIndexFields(); len(fields) > 0 {
			q.SetPayloadIndexFields(fields)
			log.Info("embed store: Qdrant", "payload_index_fields", fields)
		} else {
			log.Info("embed store: Qdrant")
		}
		embedStore = storeembed.New(q)
	} else {
		embedStore = storeembed.NewNoop()
		log.Warn("embed store: disabled (no --qdrant URL)")
	}

	var searchStore port.DocumentIndexer
	var indexerSvc *core.IndexerService
	if *opensearchURL != "" {
		osc := storeopensearch.NewClient(*opensearchURL, *opensearchIndex)
		// Retry: opensearch is sibling-container in the same compose stack and has
		// no healthcheck, so it's frequently still booting when we start. A single
		// attempt leaves search permanently disabled until the next restart.
		var ensureErr error
		for attempt := 1; attempt <= 12; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			ensureErr = osc.EnsureIndex(ctx)
			cancel()
			if ensureErr == nil {
				break
			}
			if attempt < 12 {
				log.Warn("opensearch EnsureIndex failed, retrying", "attempt", attempt, "err", ensureErr)
				time.Sleep(5 * time.Second)
			}
		}
		if ensureErr != nil {
			log.Warn("opensearch EnsureIndex failed — search disabled", "err", ensureErr)
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

	// --- MCP server ---
	em := pipeline.ResolveEmbedModel()
	mcpH, err := mcp.New(
		embedStore,
		searchStore,
		llm.GenerateEmbed,
		em,
		docs.Get,
		func(ctx context.Context, ids []string) (map[string]model.Document, error) {
			res, err := docs.ListPaginated(ctx, port.DocumentFilter{IDs: ids}, model.PageRequest{PageSize: len(ids)})
			if err != nil {
				return nil, err
			}
			out := make(map[string]model.Document, len(res.Data))
			for _, d := range res.Data {
				out[d.ID] = d
			}
			return out, nil
		},
		func(ctx context.Context, docID string) (map[string]map[string]any, error) {
			return core.CollectStageData(ctx, jobs, artifacts, fs, *vault, docID)
		},
		func(ctx context.Context, ids []string) (map[string]map[string]map[string]any, error) {
			return core.CollectStageDataBatch(ctx, jobs, artifacts, fs, *vault, ids)
		},
		5,
		defaultRAGMinScore(),
		10,
	)
	if err != nil {
		log.Error("failed to create MCP server", "err", err)
		os.Exit(1)
	}

	// Only register search_documents if indexer is available
	if searchStore == nil {
		slog.Info("MCP: search_documents disabled (no OpenSearch indexer)")
	}
	if err != nil {
		log.Error("failed to create MCP server", "err", err)
		os.Exit(1)
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

	mux := http.NewServeMux()
	mux.Handle("/", handler)
	if mcpH != nil {
		mux.Handle("/api/v1/mcp", mcpH.AuthenticatedHandler())
	}
	srv := &http.Server{Addr: *addr, Handler: mux}

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

func defaultRAGMinScore() float64 {
	if v := os.Getenv("RAG_MIN_SCORE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return 0.5
}
