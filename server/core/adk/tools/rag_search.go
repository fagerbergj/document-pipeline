// Package tools provides ADK tool implementations for the document pipeline.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
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
func NewRagSearchTool(store port.EmbedStore, embedFn EmbedFn, embedModel string) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "rag_search",
		Description: "Search the personal knowledge base for notes and documents relevant to a query. Use this when you need context about a topic, person, abbreviation, or event mentioned in the text.",
	}, func(tctx tool.Context, args RagSearchArgs) (RagSearchResult, error) {
		vec, err := embedFn(tctx, embedModel, args.Query)
		if err != nil {
			return RagSearchResult{}, fmt.Errorf("rag_search embed: %w", err)
		}

		hits, err := store.Search(tctx, vec, 5)
		if err != nil {
			return RagSearchResult{}, fmt.Errorf("rag_search query: %w", err)
		}

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

// RagSourcesFromEvents extracts retrieved chunks from tool call results so the
// caller can report them as sources. results is a slice of (query, result-JSON) pairs
// accumulated during the agent loop.
func RagSourcesFromPayloads(payloads []map[string]any) []RagChunk {
	var all []RagChunk
	for _, p := range payloads {
		raw, ok := p["results"]
		if !ok {
			continue
		}
		b, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		var chunks []RagChunk
		if err := json.Unmarshal(b, &chunks); err == nil {
			all = append(all, chunks...)
		}
	}
	return all
}

func stringVal(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
