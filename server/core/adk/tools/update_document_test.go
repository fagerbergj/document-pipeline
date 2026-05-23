package tools

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/adk/tool/toolconfirmation"

	"github.com/fagerbergj/document-pipeline/server/core/model"
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
	read := func(_ context.Context, _, _ string) (string, string, error) {
		return "old content", "clarify", nil
	}
	updateCalled := false
	update := func(_ context.Context, _, _, _ string) (string, []string, error) {
		updateCalled = true
		return "clarify", []string{"classify", "embed"}, nil
	}
	ctx := &fakeConfirmCtx{}
	res, err := runUpdateDocument(ctx, read, update, UpdateDocumentArgs{
		ID: "doc-1", Field: "clarified_text", Content: "new content",
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
	if updateCalled {
		t.Error("update fn must not be called before user approves")
	}
}

func TestUpdateDocument_ResumeApprovedRunsUpdate(t *testing.T) {
	var calledWith struct {
		docID, field, content string
	}
	update := func(_ context.Context, docID, field, content string) (string, []string, error) {
		calledWith.docID = docID
		calledWith.field = field
		calledWith.content = content
		return "clarify", []string{"classify", "embed"}, nil
	}
	read := func(_ context.Context, _, _ string) (string, string, error) {
		t.Fatal("read should not be called on resume")
		return "", "", nil
	}
	ctx := &fakeConfirmCtx{
		confirmation: &toolconfirmation.ToolConfirmation{Confirmed: true},
	}
	res, err := runUpdateDocument(ctx, read, update, UpdateDocumentArgs{
		ID: "doc-1", Field: "clarified_text", Content: "agent-proposed",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Status != "applied" {
		t.Errorf("status: want applied, got %q", res.Status)
	}
	if calledWith.content != "agent-proposed" {
		t.Errorf("update called with content %q, want agent-proposed", calledWith.content)
	}
	if len(res.DownstreamReran) != 2 {
		t.Errorf("downstream: %+v", res.DownstreamReran)
	}
}

func TestUpdateDocument_ResumeApprovedUsesPayloadContentOverride(t *testing.T) {
	var calledWith string
	update := func(_ context.Context, _, _, content string) (string, []string, error) {
		calledWith = content
		return "clarify", nil, nil
	}
	read := func(_ context.Context, _, _ string) (string, string, error) { return "", "", nil }
	ctx := &fakeConfirmCtx{
		confirmation: &toolconfirmation.ToolConfirmation{
			Confirmed: true,
			Payload:   map[string]any{"content": "user-edited"},
		},
	}
	_, err := runUpdateDocument(ctx, read, update, UpdateDocumentArgs{
		ID: "doc-1", Field: "clarified_text", Content: "agent-proposed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if calledWith != "user-edited" {
		t.Errorf("update should use payload override; got %q", calledWith)
	}
}

func TestUpdateDocument_ResumeRejectedSkipsUpdate(t *testing.T) {
	update := func(_ context.Context, _, _, _ string) (string, []string, error) {
		t.Fatal("update should not be called when rejected")
		return "", nil, nil
	}
	read := func(_ context.Context, _, _ string) (string, string, error) { return "", "", nil }
	ctx := &fakeConfirmCtx{
		confirmation: &toolconfirmation.ToolConfirmation{Confirmed: false},
	}
	res, err := runUpdateDocument(ctx, read, update, UpdateDocumentArgs{
		ID: "doc-1", Field: "clarified_text", Content: "x",
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
	_, err := runUpdateDocument(ctx, nil, nil, UpdateDocumentArgs{Field: "clarified_text", Content: "x"})
	if err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestUpdateDocument_RejectsDisallowedField(t *testing.T) {
	ctx := &fakeConfirmCtx{}
	_, err := runUpdateDocument(ctx, nil, nil, UpdateDocumentArgs{ID: "doc-1", Field: "tags", Content: "[]"})
	if err == nil {
		t.Fatal("expected error for disallowed field")
	}
}

// full_text is the name get_document uses for the body; it must be accepted and
// routed to the underlying clarified_text field.
func TestUpdateDocument_FullTextAliasRoutesToClarifiedText(t *testing.T) {
	var gotField string
	update := func(_ context.Context, _, field, _ string) (string, []string, error) {
		gotField = field
		return "clarify", nil, nil
	}
	read := func(_ context.Context, _, _ string) (string, string, error) { return "", "", nil }
	ctx := &fakeConfirmCtx{confirmation: &toolconfirmation.ToolConfirmation{Confirmed: true}}
	res, err := runUpdateDocument(ctx, read, update, UpdateDocumentArgs{
		ID: "doc-1", Field: "full_text", Content: "new body",
	})
	if err != nil {
		t.Fatalf("full_text should be accepted: %v", err)
	}
	if res.Status != "applied" {
		t.Errorf("status: want applied, got %q", res.Status)
	}
	if gotField != model.FieldClarifiedText {
		t.Errorf("update called with field %q, want %q", gotField, model.FieldClarifiedText)
	}
}

func TestUpdateDocument_RejectsEmptyContent(t *testing.T) {
	ctx := &fakeConfirmCtx{}
	_, err := runUpdateDocument(ctx, nil, nil, UpdateDocumentArgs{ID: "doc-1", Field: "clarified_text", Content: " "})
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestUpdateDocument_ReadErrorPropagates(t *testing.T) {
	read := func(_ context.Context, _, _ string) (string, string, error) {
		return "", "", errors.New("stage not done")
	}
	ctx := &fakeConfirmCtx{}
	_, err := runUpdateDocument(ctx, read, nil, UpdateDocumentArgs{ID: "doc-1", Field: "clarified_text", Content: "x"})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}
