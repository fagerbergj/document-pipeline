package tools

import (
	"context"
	"fmt"
	"log/slog"

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

// StageDataFn returns the latest outputs from each completed stage for a
// document, keyed by field name. Supplied by the wiring layer.
type StageDataFn func(ctx context.Context, id string) (map[string]map[string]any, error)

// NewSearchDocumentsTool returns an ADK tool that runs a Lucene query against
// OpenSearch and resolves each hit to a lean DocumentHit. The full content of
// any returned doc must be fetched via get_document. maxResults caps the page
// size; values <= 0 fall back to 10.
func NewSearchDocumentsTool(
	indexer port.DocumentIndexer,
	getDoc DocLookupFn,
	stageData StageDataFn,
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
		return runSearchDocuments(tctx, indexer, getDoc, stageData, maxResults, args)
	})
}

// runSearchDocuments is the tool's inner handler, factored out so it can be
// invoked from tests without constructing an ADK tool.Context.
func runSearchDocuments(
	ctx context.Context,
	indexer port.DocumentIndexer,
	getDoc DocLookupFn,
	stageData StageDataFn,
	maxResults int,
	args SearchDocumentsArgs,
) (SearchDocumentsResult, error) {
	slog.Info("search_documents", "query", args.Query, "size", maxResults)
	ids, total, err := indexer.Search(ctx, args.Query, 0, maxResults)
	if err != nil {
		return SearchDocumentsResult{}, fmt.Errorf("search_documents: %w", err)
	}
	slog.Info("search_documents hits", "query", args.Query, "got", len(ids), "total", total)

	hits := make([]DocumentHit, 0, len(ids))
	for _, id := range ids {
		doc, err := getDoc(ctx, id)
		if err != nil {
			slog.Warn("search_documents skipping missing doc", "id", id, "err", err)
			continue
		}
		h := DocumentHit{ID: doc.ID}
		if doc.Title != nil {
			h.Title = *doc.Title
		}
		if doc.DateMonth != nil {
			h.DateMonth = *doc.DateMonth
		}
		if sd, err := stageData(ctx, id); err == nil {
			h.Summary = stringFromStageData(sd, "summary")
			h.Tags = stringSliceFromStageData(sd, "tags")
		}
		hits = append(hits, h)
	}
	return SearchDocumentsResult{Results: hits}, nil
}

func stringFromStageData(sd map[string]map[string]any, field string) string {
	for _, stage := range sd {
		if v, ok := stage[field]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func stringSliceFromStageData(sd map[string]map[string]any, field string) []string {
	for _, stage := range sd {
		v, ok := stage[field]
		if !ok {
			continue
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
		}
	}
	return nil
}
