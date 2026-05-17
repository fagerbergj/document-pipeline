// Package tools provides ADK tool implementations for the document pipeline.
package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// RagSearchArgs is the input schema for the rag_search tool.
type RagSearchArgs struct {
	Query string `json:"query"`
}

// RagSearchResult is what rag_search returns to the LLM.
type RagSearchResult struct {
	Results []RagChunk `json:"results"`
}

// RagChunk is a single retrieved passage.
type RagChunk struct {
	Text      string  `json:"text"`
	Title     string  `json:"title,omitempty"`
	DateMonth string  `json:"date_month,omitempty"`
	Score     float64 `json:"score"`
}

// EmbedFn generates an embedding vector for a text query.
type EmbedFn func(ctx context.Context, model, text string) ([]float32, error)

// NewRagSearchTool returns an ADK tool that searches the vector store.
// embedFn and embedModel are used to embed the query before searching.
// maxSources caps the number of hits returned per call; values <= 0 fall back to 5.
// minScore drops hits below the threshold; values <= 0 disable the filter.
func NewRagSearchTool(store port.EmbedStore, embedFn EmbedFn, embedModel string, maxSources int, minScore float64) (tool.Tool, error) {
	if maxSources <= 0 {
		maxSources = 5
	}
	return functiontool.New(functiontool.Config{
		Name: "rag_search",
		Description: "Semantic search across the personal knowledge base. " +
			"Use for fuzzy / topical questions like \"what's been said about X?\" or when " +
			"you need passages from many docs. " +
			"Query must be PLAIN WORDS — no field:value syntax. " +
			"If your query contains a colon (e.g. `series:foo`, `tags:bar`), use " +
			"search_documents instead — this tool will match the colon literally and return junk. " +
			"Never call with an empty query.",
	}, func(tctx tool.Context, args RagSearchArgs) (RagSearchResult, error) {
		if strings.TrimSpace(args.Query) == "" {
			return RagSearchResult{}, fmt.Errorf("rag_search requires a non-empty query; pick a topic, name, or distinctive phrase and try again — do not call again with an empty query")
		}
		slog.Info("rag_search", "query", args.Query, "embed_model", embedModel, "k", maxSources, "min_score", minScore)
		vec, err := embedFn(tctx, embedModel, args.Query)
		if err != nil {
			slog.Error("rag_search embed failed", "query", args.Query, "embed_model", embedModel, "err", err)
			return RagSearchResult{}, fmt.Errorf("rag_search embed: %w", err)
		}

		hits, err := store.Search(tctx, args.Query, vec, maxSources)
		if err != nil {
			slog.Error("rag_search store query failed", "query", args.Query, "vec_dim", len(vec), "err", err)
			return RagSearchResult{}, fmt.Errorf("rag_search query: %w", err)
		}
		var topScore, bottomScore float64
		if len(hits) > 0 {
			topScore = hits[0].Score
			bottomScore = hits[len(hits)-1].Score
		}
		kept := hits
		if minScore > 0 {
			kept = kept[:0]
			for _, h := range hits {
				if h.Score >= minScore {
					kept = append(kept, h)
				}
			}
		}
		slog.Info("rag_search hits",
			"query", args.Query,
			"vec_dim", len(vec),
			"count", len(hits),
			"kept", len(kept),
			"top_score", topScore,
			"bottom_score", bottomScore,
		)
		hits = kept

		// Fetch prev/next neighbors so each result includes surrounding context.
		neighborIDs := make([]string, 0, len(hits)*2)
		for _, r := range hits {
			if id := stringVal(r.Payload, port.PayloadPrevChunk); id != "" {
				neighborIDs = append(neighborIDs, id)
			}
			if id := stringVal(r.Payload, port.PayloadNextChunk); id != "" {
				neighborIDs = append(neighborIDs, id)
			}
		}
		neighborText := map[string]string{} // chunk string ID → text
		if len(neighborIDs) > 0 {
			fetched, _ := store.GetByIDs(tctx, neighborIDs)
			for _, f := range fetched {
				neighborText[f.ID] = stringVal(f.Payload, port.PayloadText)
			}
		}

		chunks := make([]RagChunk, 0, len(hits))
		for _, r := range hits {
			var parts []string
			if t := neighborText[stringVal(r.Payload, port.PayloadPrevChunk)]; t != "" {
				parts = append(parts, t)
			}
			parts = append(parts, stringVal(r.Payload, port.PayloadText))
			if t := neighborText[stringVal(r.Payload, port.PayloadNextChunk)]; t != "" {
				parts = append(parts, t)
			}
			chunks = append(chunks, RagChunk{
				Text:      strings.Join(parts, "\n\n"),
				Title:     stringVal(r.Payload, port.PayloadTitle),
				DateMonth: stringVal(r.Payload, port.PayloadDateMonth),
				Score:     r.Score,
			})
		}
		return RagSearchResult{Results: chunks}, nil
	})
}

func stringVal(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
