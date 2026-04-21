// Package tools provides ADK tool implementations for the document pipeline.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// RagSearchArgs is the input schema for the rag_search tool.
type RagSearchArgs struct {
	Query string `json:"query" jsonschema:"Natural language query to search the knowledge base"`
	TopK  int    `json:"top_k" jsonschema:"Number of results to return (1-10)"`
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
		topK := args.TopK
		if topK <= 0 || topK > 10 {
			topK = 5
		}

		vec, err := embedFn(tctx, embedModel, args.Query)
		if err != nil {
			return RagSearchResult{}, fmt.Errorf("rag_search embed: %w", err)
		}

		results, err := store.Search(tctx, vec, topK)
		if err != nil {
			return RagSearchResult{}, fmt.Errorf("rag_search query: %w", err)
		}

		chunks := make([]RagChunk, 0, len(results))
		for _, r := range results {
			chunks = append(chunks, RagChunk{
				Text:      stringVal(r.Payload, port.PayloadText),
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
