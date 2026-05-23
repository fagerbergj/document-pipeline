package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/adk/tool/toolconfirmation"
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

// UpdateDocumentArgs is the input schema for the update_document tool. There is
// deliberately no `field`: the tool only ever rewrites the document's canonical
// body, so the model just supplies the full corrected text.
type UpdateDocumentArgs struct {
	ID      string `json:"id"`
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

// ArtifactReadFn returns the current text of the document's canonical body for
// the diff preview, along with the stage and field it resolved to.
type ArtifactReadFn func(ctx context.Context, docID string) (current, stage, field string, err error)

// ArtifactUpdateFn rewrites the document's canonical body and triggers the
// downstream cascade. Returns the stage updated and the names of downstream
// stages that were re-queued.
type ArtifactUpdateFn func(ctx context.Context, docID, content string) (stage string, downstream []string, err error)

// NewUpdateDocumentTool returns an ADK tool that lets the chat agent rewrite a
// document's canonical body (its polished clarified_text, falling back to the
// latest body stage if clarify was skipped). The tool always asks the user for
// approval via ctx.RequestConfirmation; the runtime pauses the loop until a
// FunctionResponse to adk_request_confirmation arrives from the client. On
// approve, the tool re-runs with confirmation.Confirmed=true and performs the
// actual update + downstream re-pend.
func NewUpdateDocumentTool(read ArtifactReadFn, update ArtifactUpdateFn) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:          "update_document",
		IsLongRunning: true,
		Description: "Rewrite a document's body — its polished full text (the same content " +
			"get_document returns as `full_text`). Call this whenever the user asks to fix, correct, " +
			"rewrite, or change what a note says.\n\n" +
			"Pass the document `id` and the COMPLETE corrected body as `content` (a full replacement, " +
			"not a diff or a snippet). Read the current text with get_document first so your " +
			"replacement keeps everything the user didn't ask to change.\n\n" +
			"This ALWAYS pauses and shows the user an approval card with a before/after diff before " +
			"anything is written; the user may edit your text before approving. After approval, " +
			"downstream stages (summary, tags, embeddings) automatically re-derive from the new body.\n\n" +
			"Status semantics:\n" +
			"  - pending  → user is deciding; you will see this only briefly\n" +
			"  - applied  → the edit was written and downstream stages requeued\n" +
			"  - rejected → user declined; do NOT immediately retry the same edit; ask the user what they want instead\n\n" +
			"Errors: the body stage must be 'done'. If it is still running/waiting/error, the tool " +
			"returns an error — tell the user to wait and try again.",
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
	if strings.TrimSpace(args.Content) == "" {
		return UpdateDocumentResult{}, fmt.Errorf("update_document requires non-empty content; call get_document first to see the current body, then pass the full corrected text")
	}

	if c := tctx.ToolConfirmation(); c != nil {
		if !c.Confirmed {
			slog.Info("update_document rejected", "doc_id", shortID(args.ID))
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
		stage, downstream, err := update(tctx, args.ID, content)
		if err != nil {
			return UpdateDocumentResult{}, fmt.Errorf("update_document apply: %w", err)
		}
		return UpdateDocumentResult{Status: "applied", Stage: stage, DownstreamReran: downstream}, nil
	}

	current, stage, field, err := read(tctx, args.ID)
	if err != nil {
		return UpdateDocumentResult{}, fmt.Errorf("update_document read current: %w", err)
	}
	hint := fmt.Sprintf("Replace the body of document %s?", args.ID)
	payload := map[string]any{
		"doc_id": args.ID,
		"field":  field,
		"stage":  stage,
		"before": current,
		"after":  args.Content,
	}
	if err := tctx.RequestConfirmation(hint, payload); err != nil {
		return UpdateDocumentResult{}, fmt.Errorf("update_document request confirmation: %w", err)
	}
	slog.Info("update_document pending approval",
		"doc_id", shortID(args.ID), "field", field, "stage", stage, "after_bytes", len(args.Content))
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
