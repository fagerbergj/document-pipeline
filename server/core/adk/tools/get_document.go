package tools

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/fagerbergj/document-pipeline/server/core/model"
)

// GetDocumentArgs is the input schema for the get_document tool.
type GetDocumentArgs struct {
	ID string `json:"id"`
}

// GetDocumentResult is the full content of one document returned to the LLM.
type GetDocumentResult struct {
	ID        string   `json:"id"`
	Title     string   `json:"title,omitempty"`
	FullText  string   `json:"full_text"`
	Summary   string   `json:"summary,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	DateMonth string   `json:"date_month,omitempty"`
}

// NewGetDocumentTool returns an ADK tool that fetches a single document's
// clarified_text by ID. Use after search_documents finds a candidate.
func NewGetDocumentTool(getDoc DocLookupFn, stageData StageDataFn) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "get_document",
		Description: "Fetch the full polished text (clarified_text) of a specific document by its UUID. " +
			"Use after search_documents has returned a candidate, or when the user references a doc by " +
			"an ID you already have. Also returns the doc's summary, tags, and date.",
	}, func(tctx tool.Context, args GetDocumentArgs) (GetDocumentResult, error) {
		return runGetDocument(tctx, getDoc, stageData, args)
	})
}

// runGetDocument is the tool's inner handler, factored out so it can be
// invoked from tests without constructing an ADK tool.Context.
func runGetDocument(ctx context.Context, getDoc DocLookupFn, stageData StageDataFn, args GetDocumentArgs) (GetDocumentResult, error) {
	slog.Info("get_document", "id", args.ID)
	doc, err := getDoc(ctx, args.ID)
	if err != nil {
		return GetDocumentResult{}, fmt.Errorf("get_document: %w", err)
	}
	sd, err := stageData(ctx, args.ID)
	if err != nil {
		return GetDocumentResult{}, fmt.Errorf("get_document collect stage data: %w", err)
	}
	out := GetDocumentResult{
		ID:       doc.ID,
		FullText: stringFromStageData(sd, model.StageNameClarify, model.FieldClarifiedText),
		Summary:  stringFromStageData(sd, model.StageNameClassify, model.FieldSummary),
		Tags:     tagsFromStageData(sd, model.StageNameClassify, model.FieldTags),
	}
	if doc.Title != nil {
		out.Title = *doc.Title
	}
	if doc.DateMonth != nil {
		out.DateMonth = *doc.DateMonth
	}
	return out, nil
}
