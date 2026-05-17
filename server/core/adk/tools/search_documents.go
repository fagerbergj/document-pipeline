package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// SearchDocumentsArgs is the input schema for the search_documents tool.
type SearchDocumentsArgs struct {
	Query string `json:"query"`
}

// SearchDocumentsResult is what search_documents returns to the LLM.
type SearchDocumentsResult struct {
	Results []DocumentHit `json:"results"`
}

// DocumentHit is one document summary returned from a title/content search.
// It is intentionally lean — full text comes from a follow-up get_document call.
type DocumentHit struct {
	ID        string   `json:"id"`
	Title     string   `json:"title,omitempty"`
	Summary   string   `json:"summary,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	DateMonth string   `json:"date_month,omitempty"`
}

// DocLookupFn fetches a document by ID. Supplied by the wiring layer to
// avoid an import cycle between this package and core.
type DocLookupFn func(ctx context.Context, id string) (model.Document, error)

// DocsBatchFn fetches many documents in a single round trip.
type DocsBatchFn func(ctx context.Context, ids []string) (map[string]model.Document, error)

// StageDataFn returns the latest outputs from each completed stage for a
// document, keyed by field name. Supplied by the wiring layer.
type StageDataFn func(ctx context.Context, id string) (map[string]map[string]any, error)

// StageDataBatchFn returns stage data for many documents in one query.
type StageDataBatchFn func(ctx context.Context, ids []string) (map[string]map[string]map[string]any, error)

// NewSearchDocumentsTool returns an ADK tool that runs a Lucene query against
// OpenSearch and resolves each hit to a lean DocumentHit. The full content of
// any returned doc must be fetched via get_document. maxResults caps the page
// size; values <= 0 fall back to 10.
//
// Doc and stage-data lookups are batched (one round trip each across all
// hits) so an N-hit search costs O(1) repo queries, not O(N).
func NewSearchDocumentsTool(
	indexer port.DocumentIndexer,
	getDocs DocsBatchFn,
	stageDataBatch StageDataBatchFn,
	maxResults int,
) (tool.Tool, error) {
	if maxResults <= 0 {
		maxResults = 10
	}
	return functiontool.New(functiontool.Config{
		Name: "search_documents",
		Description: "Search the personal knowledge base for documents by title, tag, content, date, or series. " +
			"Returns lean summaries — use get_document to fetch a specific doc's full text. " +
			"Query syntax is Lucene: bare words are full-text; `title:foo` / `tags:invoice` / " +
			"`series:notebooks` / `date_month:2026-05` / `stage:done` filter to specific fields; " +
			"combine with `AND` / `OR`. Use this when the user mentions a specific document by " +
			"name, date, or topic that lives in one place.",
	}, func(tctx tool.Context, args SearchDocumentsArgs) (SearchDocumentsResult, error) {
		return runSearchDocuments(tctx, indexer, getDocs, stageDataBatch, maxResults, args)
	})
}

// runSearchDocuments is the tool's inner handler, factored out so it can be
// invoked from tests without constructing an ADK tool.Context.
func runSearchDocuments(
	ctx context.Context,
	indexer port.DocumentIndexer,
	getDocs DocsBatchFn,
	stageDataBatch StageDataBatchFn,
	maxResults int,
	args SearchDocumentsArgs,
) (SearchDocumentsResult, error) {
	slog.Info("search_documents", "query", args.Query, "size", maxResults)
	ids, total, err := indexer.Search(ctx, args.Query, 0, maxResults)
	if err != nil {
		return SearchDocumentsResult{}, fmt.Errorf("search_documents: %w", err)
	}
	slog.Info("search_documents hits", "query", args.Query, "got", len(ids), "total", total)
	if len(ids) == 0 {
		return SearchDocumentsResult{Results: []DocumentHit{}}, nil
	}

	docs, err := getDocs(ctx, ids)
	if err != nil {
		return SearchDocumentsResult{}, fmt.Errorf("search_documents: bulk fetch docs: %w", err)
	}
	stageMap, err := stageDataBatch(ctx, ids)
	if err != nil {
		// Degrade: skip metadata enrichment rather than fail the whole tool call.
		slog.Warn("search_documents stage data batch failed; returning lean hits", "err", err)
		stageMap = nil
	}

	hits := make([]DocumentHit, 0, len(ids))
	for _, id := range ids {
		doc, ok := docs[id]
		if !ok {
			slog.Warn("search_documents skipping missing doc", "id", id)
			continue
		}
		h := DocumentHit{ID: doc.ID}
		if doc.Title != nil {
			h.Title = *doc.Title
		}
		if doc.DateMonth != nil {
			h.DateMonth = *doc.DateMonth
		}
		if sd, ok := stageMap[id]; ok {
			h.Summary = stringFromStageData(sd, model.StageNameClassify, model.FieldSummary)
			h.Tags = tagsFromStageData(sd, model.StageNameClassify, model.FieldTags)
		}
		hits = append(hits, h)
	}
	return SearchDocumentsResult{Results: hits}, nil
}

// stringFromStageData reads a string output field from a specific stage's
// results. Scoping by stage avoids ambiguity when two stages happen to emit
// the same field name.
func stringFromStageData(sd map[string]map[string]any, stage, field string) string {
	v, ok := sd[stage][field]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// tagsFromStageData reads a tag list. classify emits tags as a JSON-encoded
// string (the raw artifact contents); production stage data therefore stores
// it as a string, not a []string. Test fixtures sometimes pre-parse, so we
// accept both shapes.
func tagsFromStageData(sd map[string]map[string]any, stage, field string) []string {
	v, ok := sd[stage][field]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		raw := strings.TrimSpace(t)
		if raw == "" {
			return nil
		}
		var tags []string
		if err := json.Unmarshal([]byte(raw), &tags); err != nil {
			return nil
		}
		return tags
	}
	return nil
}
