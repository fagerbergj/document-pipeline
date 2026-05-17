package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/adk/tool/toolconfirmation"

	"github.com/fagerbergj/document-pipeline/server/core/model"
)

// confirmContext is the subset of tool.Context behavior runUpdateDocument
// uses: the underlying context, the confirmation-request initiator, and
// the resumed-state reader. The real tool.Context satisfies this; tests
// implement it directly without standing up an ADK runtime.
type confirmContext interface {
	context.Context
	RequestConfirmation(hint string, payload any) error
	ToolConfirmation() *toolconfirmation.ToolConfirmation
}

// UpdateDocumentArgs is the input schema for the update_document tool.
type UpdateDocumentArgs struct {
	ID      string `json:"id"`
	Field   string `json:"field"`
	Content string `json:"content"`
}

// UpdateDocumentResult is what update_document returns to the LLM after the
// user has either approved (status="applied") or rejected (status="rejected")
// the proposed change. While the user is still deciding, ADK pauses the agent
// loop — the tool returns {status:"pending"} which the runtime swallows
// internally and surfaces as an adk_request_confirmation event to the client.
type UpdateDocumentResult struct {
	Status          string   `json:"status"` // "pending" | "applied" | "rejected"
	Stage           string   `json:"stage,omitempty"`
	DownstreamReran []string `json:"downstream_reran,omitempty"`
}

// ArtifactReadFn returns the current text of a stage output for diff preview.
type ArtifactReadFn func(ctx context.Context, docID, field string) (current string, stage string, err error)

// ArtifactUpdateFn writes new content for a stage output and triggers the
// downstream cascade. Returns the stage updated and the names of downstream
// stages that were re-queued.
type ArtifactUpdateFn func(ctx context.Context, docID, field, content string) (stage string, downstream []string, err error)

// allowedUpdateFields enumerates the fields the chat tool may edit. Mirrors
// the scope-locked subset from the design doc — tags excluded for v1.
var allowedUpdateFields = map[string]bool{
	model.FieldRawText:          true,
	model.FieldNarrativeSummary: true,
	model.FieldClarifiedText:    true,
	model.FieldSummary:          true,
}

// NewUpdateDocumentTool returns an ADK tool that lets the chat agent propose
// an edit to one of a document's stage outputs. The tool always asks the
// user for approval via ctx.RequestConfirmation; the runtime pauses the loop
// until a FunctionResponse to adk_request_confirmation arrives from the
// client. On approve, the tool re-runs with confirmation.Confirmed=true and
// performs the actual update + downstream re-pend.
func NewUpdateDocumentTool(read ArtifactReadFn, update ArtifactUpdateFn) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:          "update_document",
		IsLongRunning: true,
		Description: "Propose an edit to a specific stage output of a document. Requires user approval " +
			"before applying — the runtime will pause and show the user a confirmation card with the " +
			"before/after diff. The user may edit your proposed content before approving.\n\n" +
			"Allowed `field` values:\n" +
			"  - raw_text          (transcribe/ocr output)\n" +
			"  - narrative_summary (summarize output)\n" +
			"  - clarified_text    (clarify output)\n" +
			"  - summary           (classify output — not the narrative one)\n\n" +
			"After approval, downstream stages (including embed) automatically re-run, so the corrected " +
			"text propagates through the pipeline.\n\n" +
			"Status semantics:\n" +
			"  - pending  → user is deciding; you will see this only briefly\n" +
			"  - applied  → the edit was written and downstream stages requeued\n" +
			"  - rejected → user declined; do NOT immediately retry the same edit; ask the user what they want instead\n\n" +
			"Errors: the stage must be in 'done' status. If the stage is still running/waiting/error, " +
			"the tool returns an error — tell the user to wait and try again.",
	}, func(tctx tool.Context, args UpdateDocumentArgs) (UpdateDocumentResult, error) {
		return runUpdateDocument(tctx, read, update, args)
	})
}

// runUpdateDocument is invoked twice for each edit: once on the model's
// initial tool call (no ToolConfirmation present — we request approval and
// return "pending") and once after ADK resumes the loop with the user's
// FunctionResponse (ToolConfirmation populated — we apply or reject).
func runUpdateDocument(tctx confirmContext, read ArtifactReadFn, update ArtifactUpdateFn, args UpdateDocumentArgs) (UpdateDocumentResult, error) {
	if strings.TrimSpace(args.ID) == "" {
		return UpdateDocumentResult{}, fmt.Errorf("update_document requires an id; pass the document UUID")
	}
	if !allowedUpdateFields[args.Field] {
		return UpdateDocumentResult{}, fmt.Errorf("update_document field %q is not editable; allowed: raw_text, narrative_summary, clarified_text, summary", args.Field)
	}
	if strings.TrimSpace(args.Content) == "" {
		return UpdateDocumentResult{}, fmt.Errorf("update_document requires non-empty content; use rag_search/get_document first to see the current value, then propose a replacement")
	}

	if c := tctx.ToolConfirmation(); c != nil {
		if !c.Confirmed {
			slog.Info("update_document rejected", "doc_id", shortID(args.ID), "field", args.Field)
			return UpdateDocumentResult{Status: "rejected"}, nil
		}
		// The user may have edited the proposed content before approving.
		// Prefer the payload-supplied content over the model's original args.
		content := args.Content
		if p, ok := c.Payload.(map[string]any); ok {
			if v, ok := p["content"].(string); ok && v != "" {
				content = v
			}
		}
		stage, downstream, err := update(tctx, args.ID, args.Field, content)
		if err != nil {
			return UpdateDocumentResult{}, fmt.Errorf("update_document apply: %w", err)
		}
		return UpdateDocumentResult{Status: "applied", Stage: stage, DownstreamReran: downstream}, nil
	}

	current, stage, err := read(tctx, args.ID, args.Field)
	if err != nil {
		return UpdateDocumentResult{}, fmt.Errorf("update_document read current: %w", err)
	}
	hint := fmt.Sprintf("Replace %s for document %s?", args.Field, args.ID)
	payload := map[string]any{
		"doc_id": args.ID,
		"field":  args.Field,
		"stage":  stage,
		"before": current,
		"after":  args.Content,
	}
	if err := tctx.RequestConfirmation(hint, payload); err != nil {
		return UpdateDocumentResult{}, fmt.Errorf("update_document request confirmation: %w", err)
	}
	slog.Info("update_document pending approval",
		"doc_id", shortID(args.ID), "field", args.Field, "stage", stage, "after_bytes", len(args.Content))
	return UpdateDocumentResult{Status: "pending", Stage: stage}, nil
}

// shortID returns the first 8 chars of s for logging, or the full string if
// shorter. Avoids out-of-bounds panics on stub/test IDs.
func shortID(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}
