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
	// IncludeStageOutputs adds the intermediate, non-canonical stage outputs
	// (raw_text, narrative_summary) to the result. Off by default so normal
	// Q&A only ever sees the canonical body and never quotes noisy capture text
	// or an intermediate draft. The model sets it only when the user wants to
	// inspect or edit an earlier stage.
	IncludeStageOutputs bool `json:"include_stage_outputs,omitempty"`
}

// GetDocumentResult is the content of one document returned to the LLM.
// FullText is the clarify stage's clarified_text — the canonical polished body
// the user reads, and what normal answers should quote. RawText and
// NarrativeSummary are the intermediate stage outputs; they are populated only
// when GetDocumentArgs.IncludeStageOutputs is set (used for the edit flow).
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
		Description: "Fetch a document's contents by its UUID. By default returns the canonical body " +
			"`full_text` (the polished version the user reads — quote THIS when answering) plus " +
			"`summary`, `tags`, and date.\n" +
			"Set `include_stage_outputs: true` ONLY when the user wants to inspect an earlier " +
			"pipeline stage — it adds the intermediate, non-canonical `raw_text` (transcribe/ocr) " +
			"and `narrative_summary` (summarize). These are for reference; do not request them for " +
			"normal questions, as they are noisier and may contradict the polished body.\n" +
			"Use after search_documents returns a candidate id, or when the user references a doc " +
			"by an id you already have. Never call with an empty id — run search_documents first.",
	}, func(tctx tool.Context, args GetDocumentArgs) (GetDocumentResult, error) {
		return RunGetDocument(tctx, getDoc, stageData, args)
	})
}

// RunGetDocument is the tool's inner handler, factored out so it can be
// invoked from tests or MCP without constructing an ADK tool.Context.
func RunGetDocument(ctx context.Context, getDoc DocLookupFn, stageData StageDataFn, args GetDocumentArgs) (GetDocumentResult, error) {
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
	// full_text is the canonical body (clarify's clarified_text), but clarify
	// may have been skipped (e.g. uploaded without context, or below the
	// summarize size threshold). Fall back to the most-polished body that did
	// run so the model never gets an empty document and silently fails to answer.
	fullText := stagefield.String(sd, model.StageNameClarify, model.FieldClarifiedText)
	if fullText == "" {
		fullText = stagefield.String(sd, model.StageNameSummarize, model.FieldNarrativeSummary)
	}
	if fullText == "" {
		fullText = rawTextOf(sd)
	}
	out := GetDocumentResult{
		ID:       doc.ID,
		FullText: fullText,
		Summary:  stagefield.String(sd, model.StageNameClassify, model.FieldSummary),
		Tags:     stagefield.Tags(sd, model.StageNameClassify, model.FieldTags),
	}
	// Intermediate stage outputs are opt-in: normal answers should only see the
	// canonical body, not the noisier capture text or the intermediate draft.
	if args.IncludeStageOutputs {
		out.RawText = rawTextOf(sd)
		out.NarrativeSummary = stagefield.String(sd, model.StageNameSummarize, model.FieldNarrativeSummary)
	}
	if doc.Title != nil {
		out.Title = *doc.Title
	}
	if doc.DateMonth != nil {
		out.DateMonth = *doc.DateMonth
	}
	return out, nil
}

// rawTextOf returns the raw_text produced by whichever capture stage ran for
// this doc: transcribe for audio, ocr for images. Only one runs, so fall back.
func rawTextOf(sd model.StageOutputs) string {
	if t := stagefield.String(sd, model.StageNameTranscribe, model.FieldRawText); t != "" {
		return t
	}
	return stagefield.String(sd, model.StageNameOCR, model.FieldRawText)
}
