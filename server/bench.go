package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/fagerbergj/document-pipeline/server/core/port"
	"github.com/fagerbergj/document-pipeline/server/store/config"
	storeembed "github.com/fagerbergj/document-pipeline/server/store/embed"
	"github.com/fagerbergj/document-pipeline/server/store/openai"
	"github.com/fagerbergj/document-pipeline/server/store/qdrant"
)

// runBenchEmbed runs each query through the SAME path the live rag_search tool
// uses — openai.GenerateEmbed for the query vector, then EmbedStore.Search for
// retrieval — and prints the score/title/snippet of every hit.
//
// Because it calls the real store.Search, the scores are the production HYBRID
// scores (dense + BM25 sparse, RRF-fused), not the dense-only cosine the shell
// bench reports. Those are the numbers to use when tuning RAG_MIN_SCORE.
//
// Qdrant has no host port in production (it lives only on the llm network), so
// run this inside the document-pipeline container, where the binary already has
// the right env and network:
//
//	docker exec document-pipeline document-pipeline -bench -bench-topk 5 \
//	  "Who is Zuk Bugbag and what did he co-found?" "What ship is the party on?"
//
// The query is embedded raw — identical to rag_search.go — so there is no
// instruction-prefix knob here on purpose: this mirrors production exactly.
func runBenchEmbed(log *slog.Logger, pipelineCfg, llmURL, qdrantURL, qdrantCollection, qdrantKey string, topK int, queries []string) {
	if qdrantURL == "" {
		log.Error("bench: --qdrant/QDRANT_URL is required")
		os.Exit(1)
	}
	if len(queries) == 0 {
		log.Error("bench: provide one or more queries as positional arguments")
		os.Exit(1)
	}
	if topK <= 0 {
		topK = 5
	}

	// Only the embed model is needed from the pipeline; reuse the same resolver
	// the worker and chat handler use so the bench can't drift from them.
	pipeline, err := (&config.YAMLPipelineSource{Path: pipelineCfg}).Load()
	if err != nil {
		log.Error("bench: load pipeline config", "err", err)
		os.Exit(1)
	}
	embedModel := pipeline.ResolveEmbedModel()

	llm := openai.New(llmURL, envOr("LLM_API_KEY", ""))
	store := storeembed.New(qdrant.New(qdrantURL, qdrantCollection, qdrantKey))

	ctx := context.Background()
	fmt.Printf("model=%s  collection=%s  top_k=%d  (hybrid dense+BM25, RRF — same as rag_search)\n\n",
		embedModel, qdrantCollection, topK)

	for _, q := range queries {
		fmt.Printf("── query: %s\n", q)
		vec, err := llm.GenerateEmbed(ctx, embedModel, q)
		if err != nil {
			fmt.Printf("  embed failed: %v\n\n", err)
			continue
		}
		hits, err := store.Search(ctx, q, vec, topK)
		if err != nil {
			fmt.Printf("  search failed: %v\n\n", err)
			continue
		}
		if len(hits) == 0 {
			fmt.Printf("  (no hits)\n\n")
			continue
		}
		for _, h := range hits {
			fmt.Printf("  %.4f  %s — %s\n",
				h.Score,
				payloadStr(h.Payload, port.PayloadTitle),
				snippet(payloadStr(h.Payload, port.PayloadText), 80))
		}
		fmt.Println()
	}
}

func payloadStr(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// snippet collapses whitespace and truncates to n runes for a readable table.
func snippet(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
