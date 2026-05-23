package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"github.com/fagerbergj/document-pipeline/server/core/stagefield"
)

// GetDocumentArgs is the input schema for the get_document tool.
type GetDocumentArgs struct {
	ID string `json:"id"`
}

// GetDocumentResult is the full content of one document returned to the LLM.
// FullText is the clarify stage's clarified_text — the polished body the user
// reads. RawText and NarrativeSummary expose the earlier-stage outputs so the
// model can see every field update_document can edit (full_text/clarified_text,
// narrative_summary, raw_text, summary).
type GetDocumentResult struct {
	ID               string   `json:"id"`
	Title            string   `json:"title,omitempty"`
	FullText         string   `json:"full_text"`
	NarrativeSummary string   `json:"narrative_summary,omitempty"`
	RawText          string   `json:"raw_text,omitempty"`
	Summary          string   `json:"summary,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	DateMonth        string   `json:"date_month,omitempty"`
}

// NewGetDocumentTool returns an ADK tool that fetches a single document's
// clarified_text by ID. Use after search_documents finds a candidate.
func NewGetDocumentTool(getDoc DocLookupFn, stageData StageDataFn) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "get_document",
		Description: "Fetch a document's contents by its UUID. Returns `full_text` (the polished body, " +
			"i.e. the clarify stage's clarified_text — pass `full_text` or `clarified_text` to " +
			"update_document to edit it), plus `narrative_summary`, `raw_text`, `summary`, `tags`, " +
			"and date. Every returned text field except tags/date is editable via update_document. " +
			"Use after search_documents returns a candidate id, or when the user references a doc " +
			"by an id you already have. Never call with an empty id — run search_documents first.",
	}, func(tctx tool.Context, args GetDocumentArgs) (GetDocumentResult, error) {
		return runGetDocument(tctx, getDoc, stageData, args)
	})
}

// runGetDocument is the tool's inner handler, factored out so it can be
// invoked from tests without constructing an ADK tool.Context.
func runGetDocument(ctx context.Context, getDoc DocLookupFn, stageData StageDataFn, args GetDocumentArgs) (GetDocumentResult, error) {
	if strings.TrimSpace(args.ID) == "" {
		return GetDocumentResult{}, fmt.Errorf("get_document requires an id; call search_documents first to find a candidate id, then pass it here — do not call again with an empty id")
	}
	slog.Info("get_document", "id", args.ID)
	doc, err := getDoc(ctx, args.ID)
	if err != nil {
		return GetDocumentResult{}, fmt.Errorf("get_document: %w", err)
	}
	sd, err := stageData(ctx, args.ID)
	if err != nil {
		return GetDocumentResult{}, fmt.Errorf("get_document collect stage data: %w", err)
	}
	// raw_text is produced by whichever capture stage ran for this doc's type:
	// transcribe for audio, ocr for images. Only one runs, so fall back.
	rawText := stagefield.String(sd, model.StageNameTranscribe, model.FieldRawText)
	if rawText == "" {
		rawText = stagefield.String(sd, model.StageNameOCR, model.FieldRawText)
	}
	out := GetDocumentResult{
		ID:               doc.ID,
		FullText:         stagefield.String(sd, model.StageNameClarify, model.FieldClarifiedText),
		NarrativeSummary: stagefield.String(sd, model.StageNameSummarize, model.FieldNarrativeSummary),
		RawText:          rawText,
		Summary:          stagefield.String(sd, model.StageNameClassify, model.FieldSummary),
		Tags:             stagefield.Tags(sd, model.StageNameClassify, model.FieldTags),
	}
	if doc.Title != nil {
		out.Title = *doc.Title
	}
	if doc.DateMonth != nil {
		out.DateMonth = *doc.DateMonth
	}
	return out, nil
}
