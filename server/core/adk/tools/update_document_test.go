package tools

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/adk/tool/toolconfirmation"
)

// fakeConfirmCtx implements confirmContext for the two-phase tool flow. The
// `confirmation` field controls which phase a given Run call simulates: nil
// means "first call, agent has not yet asked for confirmation"; non-nil
// means "resumed with the user's decision".
type fakeConfirmCtx struct {
	confirmation *toolconfirmation.ToolConfirmation
	// requested is populated when the handler calls RequestConfirmation.
	requested *struct {
		hint    string
		payload any
	}
	// requestErr, if set, is returned from RequestConfirmation to simulate
	// ADK rejecting the pause request.
	requestErr error
}

func (f *fakeConfirmCtx) Deadline() (time.Time, bool)                          { return time.Time{}, false }
func (f *fakeConfirmCtx) Done() <-chan struct{}                                { return nil }
func (f *fakeConfirmCtx) Err() error                                           { return nil }
func (f *fakeConfirmCtx) Value(_ any) any                                      { return nil }
func (f *fakeConfirmCtx) ToolConfirmation() *toolconfirmation.ToolConfirmation { return f.confirmation }
func (f *fakeConfirmCtx) RequestConfirmation(hint string, payload any) error {
	if f.requestErr != nil {
		return f.requestErr
	}
	f.requested = &struct {
		hint    string
		payload any
	}{hint, payload}
	return nil
}

func TestUpdateDocument_FirstCallRequestsConfirmation(t *testing.T) {
	read := func(_ context.Context, _ string) (string, string, string, error) {
		return "old content", "clarify", "clarified_text", nil
	}
	updateCalled := false
	update := func(_ context.Context, _, _, _, _ string) ([]string, error) {
		updateCalled = true
		return []string{"classify", "embed"}, nil
	}
	ctx := &fakeConfirmCtx{}
	res, err := runUpdateDocument(ctx, read, update, UpdateDocumentArgs{
		ID: "doc-1", Content: "new content",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Status != "pending" {
		t.Errorf("status: want pending, got %q", res.Status)
	}
	if res.Stage != "clarify" {
		t.Errorf("stage: want clarify, got %q", res.Stage)
	}
	if ctx.requested == nil {
		t.Fatal("RequestConfirmation was not called")
	}
	pl, ok := ctx.requested.payload.(map[string]any)
	if !ok {
		t.Fatalf("payload type: %T", ctx.requested.payload)
	}
	if pl["before"] != "old content" || pl["after"] != "new content" {
		t.Errorf("payload before/after: %+v", pl)
	}
	if pl["field"] != "clarified_text" {
		t.Errorf("payload field: %+v", pl)
	}
	if updateCalled {
		t.Error("update fn must not be called before user approves")
	}
}

func TestUpdateDocument_ResumeApprovedRunsUpdate(t *testing.T) {
	var calledWith struct {
		docID, stage, field, content string
	}
	update := func(_ context.Context, docID, stage, field, content string) ([]string, error) {
		calledWith.docID = docID
		calledWith.stage = stage
		calledWith.field = field
		calledWith.content = content
		return []string{"classify", "embed"}, nil
	}
	read := func(_ context.Context, _ string) (string, string, string, error) {
		t.Fatal("read should not be called on resume when the payload pins stage/field")
		return "", "", "", nil
	}
	// The pinned stage/field from the request ride along on the payload.
	ctx := &fakeConfirmCtx{
		confirmation: &toolconfirmation.ToolConfirmation{
			Confirmed: true,
			Payload:   map[string]any{"stage": "clarify", "field": "clarified_text"},
		},
	}
	res, err := runUpdateDocument(ctx, read, update, UpdateDocumentArgs{
		ID: "doc-1", Content: "agent-proposed",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Status != "applied" {
		t.Errorf("status: want applied, got %q", res.Status)
	}
	if res.Stage != "clarify" {
		t.Errorf("result stage: want clarify, got %q", res.Stage)
	}
	if calledWith.content != "agent-proposed" {
		t.Errorf("update called with content %q, want agent-proposed", calledWith.content)
	}
	if calledWith.stage != "clarify" || calledWith.field != "clarified_text" {
		t.Errorf("update should apply the pinned stage/field; got stage=%q field=%q", calledWith.stage, calledWith.field)
	}
	if len(res.DownstreamReran) != 2 {
		t.Errorf("downstream: %+v", res.DownstreamReran)
	}
}

// When the payload pins the stage/field, the apply must NOT re-resolve the
// canonical body — so a body stage finishing between request and approval can't
// redirect the write to a different field than the user reviewed.
func TestUpdateDocument_ResumeAppliesPinnedFieldNotReResolved(t *testing.T) {
	var gotField string
	update := func(_ context.Context, _, _, field, _ string) ([]string, error) {
		gotField = field
		return nil, nil
	}
	read := func(_ context.Context, _ string) (string, string, string, error) {
		t.Fatal("apply must use the pinned field, not re-read/re-resolve")
		return "", "", "", nil
	}
	ctx := &fakeConfirmCtx{
		confirmation: &toolconfirmation.ToolConfirmation{
			Confirmed: true,
			Payload:   map[string]any{"stage": "summarize", "field": "narrative_summary"},
		},
	}
	if _, err := runUpdateDocument(ctx, read, update, UpdateDocumentArgs{ID: "doc-1", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if gotField != "narrative_summary" {
		t.Errorf("apply wrote field %q, want the pinned narrative_summary", gotField)
	}
}

func TestUpdateDocument_ResumeApprovedUsesPayloadContentOverride(t *testing.T) {
	var calledWith string
	update := func(_ context.Context, _, _, _, content string) ([]string, error) {
		calledWith = content
		return nil, nil
	}
	read := func(_ context.Context, _ string) (string, string, string, error) { return "", "", "", nil }
	ctx := &fakeConfirmCtx{
		confirmation: &toolconfirmation.ToolConfirmation{
			Confirmed: true,
			Payload:   map[string]any{"content": "user-edited", "stage": "clarify", "field": "clarified_text"},
		},
	}
	_, err := runUpdateDocument(ctx, read, update, UpdateDocumentArgs{
		ID: "doc-1", Content: "agent-proposed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if calledWith != "user-edited" {
		t.Errorf("update should use payload override; got %q", calledWith)
	}
}

func TestUpdateDocument_ResumeRejectedSkipsUpdate(t *testing.T) {
	update := func(_ context.Context, _, _, _, _ string) ([]string, error) {
		t.Fatal("update should not be called when rejected")
		return nil, nil
	}
	read := func(_ context.Context, _ string) (string, string, string, error) { return "", "", "", nil }
	ctx := &fakeConfirmCtx{
		confirmation: &toolconfirmation.ToolConfirmation{Confirmed: false},
	}
	res, err := runUpdateDocument(ctx, read, update, UpdateDocumentArgs{
		ID: "doc-1", Content: "x",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Status != "rejected" {
		t.Errorf("status: want rejected, got %q", res.Status)
	}
}

func TestUpdateDocument_RejectsEmptyID(t *testing.T) {
	ctx := &fakeConfirmCtx{}
	_, err := runUpdateDocument(ctx, nil, nil, UpdateDocumentArgs{Content: "x"})
	if err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestUpdateDocument_RejectsEmptyContent(t *testing.T) {
	ctx := &fakeConfirmCtx{}
	_, err := runUpdateDocument(ctx, nil, nil, UpdateDocumentArgs{ID: "doc-1", Content: " "})
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestUpdateDocument_ReadErrorPropagates(t *testing.T) {
	read := func(_ context.Context, _ string) (string, string, string, error) {
		return "", "", "", errors.New("stage not done")
	}
	ctx := &fakeConfirmCtx{}
	_, err := runUpdateDocument(ctx, read, nil, UpdateDocumentArgs{ID: "doc-1", Content: "x"})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}
