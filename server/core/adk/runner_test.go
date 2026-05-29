package adk

import (
	"context"
	"encoding/json"
	"testing"

	"google.golang.org/adk/session"
	"google.golang.org/adk/tool/toolconfirmation"
	"google.golang.org/genai"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// buildConfirmationRequest must surface the hint + payload whether ADK gives us
// the freshly emitted ToolConfirmation struct (in-memory streaming path) or the
// map[string]any it becomes after JSON session persistence. Asserting only the
// map form silently dropped the approval card's diff on the live path.
func TestBuildConfirmationRequest_PopulatesHintAndPayload(t *testing.T) {
	payload := map[string]any{"before": "old", "after": "new", "field": "clarified_text", "stage": "clarify"}
	structFC := &genai.FunctionCall{
		ID:   "call-1",
		Name: toolconfirmation.FunctionCallName,
		Args: map[string]any{
			"toolConfirmation": toolconfirmation.ToolConfirmation{Hint: "Replace the body?", Payload: payload},
		},
	}

	assertConfirmation(t, "struct", buildConfirmationRequest(structFC), payload)

	// Simulate a session round-trip: args["toolConfirmation"] becomes a map.
	raw, err := json.Marshal(structFC.Args)
	if err != nil {
		t.Fatal(err)
	}
	var mapArgs map[string]any
	if err := json.Unmarshal(raw, &mapArgs); err != nil {
		t.Fatal(err)
	}
	mapFC := &genai.FunctionCall{ID: "call-1", Name: toolconfirmation.FunctionCallName, Args: mapArgs}
	assertConfirmation(t, "map", buildConfirmationRequest(mapFC), payload)
}

func assertConfirmation(t *testing.T, label string, ev StreamEvent, wantPayload map[string]any) {
	t.Helper()
	if ev.CallID != "call-1" {
		t.Errorf("%s: call id %q", label, ev.CallID)
	}
	if ev.Hint != "Replace the body?" {
		t.Errorf("%s: hint %q, want non-empty", label, ev.Hint)
	}
	pm, ok := ev.Payload.(map[string]any)
	if !ok {
		t.Fatalf("%s: payload type %T, want map", label, ev.Payload)
	}
	for k, want := range wantPayload {
		if pm[k] != want {
			t.Errorf("%s: payload[%q] = %v, want %v", label, k, pm[k], want)
		}
	}
}

type fakeLLM struct {
	resp port.LLMChatResponse
}

func (f *fakeLLM) GenerateVision(_ context.Context, _, _ string, _ []byte, _ func(string)) error {
	return nil
}
func (f *fakeLLM) GenerateText(_ context.Context, _, _ string, _ func(string)) error { return nil }
func (f *fakeLLM) ChatWithTools(_ context.Context, _ string, _ []port.LLMMessage, _ []port.LLMTool) (port.LLMChatResponse, error) {
	return f.resp, nil
}
func (f *fakeLLM) ChatStream(_ context.Context, _ string, _ []port.LLMMessage, _ func(string)) error {
	return nil
}
func (f *fakeLLM) GenerateEmbed(_ context.Context, _, _ string) ([]float32, error) {
	return nil, nil
}

// When the LLM returns out-of-band Thinking, RunAgent must surface it as a
// StreamEventThinking — not folded into the token stream or finalText.
func TestRunAgent_SurfacesThinkingSeparately(t *testing.T) {
	llm := &fakeLLM{resp: port.LLMChatResponse{
		Thinking: "let me think about this",
		Text:     "here is the answer",
	}}
	mdl := NewPortLLMModel(llm, "test-model")

	var events []StreamEvent
	res, err := RunAgent(
		context.Background(),
		mdl,
		nil,
		"instruction",
		[]*genai.Part{{Text: "hello"}},
		session.InMemoryService(),
		PipelineUserID,
		"sess-1",
		func(ev StreamEvent) { events = append(events, ev) },
	)
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}

	var thinking, token []string
	for _, ev := range events {
		switch ev.Kind {
		case StreamEventThinking:
			thinking = append(thinking, ev.Text)
		case StreamEventToken:
			token = append(token, ev.Text)
		}
	}
	if len(thinking) != 1 || thinking[0] != "let me think about this" {
		t.Errorf("thinking events = %v, want [\"let me think about this\"]", thinking)
	}
	if len(token) != 1 || token[0] != "here is the answer" {
		t.Errorf("token events = %v, want [\"here is the answer\"]", token)
	}
	if res.Text != "here is the answer" {
		t.Errorf("RunResult.Text = %q, want %q (thinking must not bleed into final text)", res.Text, "here is the answer")
	}
}

func TestStreamEvent_SSEEventType_UsesPortConstants(t *testing.T) {
	cases := []struct {
		kind StreamEventKind
		want string
	}{
		{StreamEventToken, port.EventToken},
		{StreamEventThinking, port.EventThinking},
		{StreamEventToolCall, port.EventToolCall},
		{StreamEventToolResult, port.EventToolResult},
		{StreamEventConfirmationRequest, port.EventConfirmationRequest},
	}
	for _, c := range cases {
		got := StreamEvent{Kind: c.kind}.SSEEventType()
		if got != c.want {
			t.Errorf("kind %q → %q, want %q", c.kind, got, c.want)
		}
	}
}

// RequestedConfirmationPayload must recover the request-time payload (e.g. the
// resolved stage/field) from the pending confirmation FunctionCall in the
// session, so confirmChatToolCall can echo it into the user's response — ADK
// otherwise drops it on resume.
func TestRequestedConfirmationPayload_RecoversStageField(t *testing.T) {
	ctx := context.Background()
	svc := session.InMemoryService()
	resp, err := svc.Create(ctx, &session.CreateRequest{AppName: AppName, UserID: PipelineUserID, SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	sess := resp.Session

	payload := map[string]any{"field": "clarified_text", "stage": "clarify", "before": "x", "after": "y"}
	ev := session.NewEvent("ev-1")
	ev.Author = "model"
	ev.Content = &genai.Content{
		Role: genai.RoleModel,
		Parts: []*genai.Part{{
			FunctionCall: &genai.FunctionCall{
				ID:   "call-1",
				Name: toolconfirmation.FunctionCallName,
				Args: map[string]any{"toolConfirmation": toolconfirmation.ToolConfirmation{Hint: "h", Payload: payload}},
			},
		}},
	}
	if err := svc.AppendEvent(ctx, sess, ev); err != nil {
		t.Fatal(err)
	}

	got, ok := RequestedConfirmationPayload(sess, "call-1")
	if !ok {
		t.Fatal("expected to recover the request payload")
	}
	if got["stage"] != "clarify" || got["field"] != "clarified_text" {
		t.Errorf("recovered payload = %v, want stage=clarify field=clarified_text", got)
	}

	if _, ok := RequestedConfirmationPayload(sess, "no-such-call"); ok {
		t.Error("expected (nil, false) for an unknown call id")
	}
	if _, ok := RequestedConfirmationPayload(nil, "call-1"); ok {
		t.Error("expected (nil, false) for a nil session")
	}
}

// Regression: the session helpers (RunAgent, AppendStateEvent) must operate on
// the (userID, sessionID) tuple they're given, not a package-level default.
//
// The earlier bug: every helper used PipelineUserID internally. Chat handlers
// would create a session under the request user's UID, then call RunAgent
// which silently wrote events to a phantom session under PipelineUserID — so
// reloading the chat returned no messages and titles never landed. This test
// drives the helpers with two distinct userIDs sharing the same sessionID and
// asserts they stay isolated.
func TestSessionHelpers_HonorUserID(t *testing.T) {
	ctx := context.Background()
	svc := session.InMemoryService()
	const sessID = "shared-session-id"
	const userA = "user-a-uid"
	const userB = "user-b-uid"

	llm := &fakeLLM{resp: port.LLMChatResponse{Text: "reply"}}
	mdl := NewPortLLMModel(llm, "test-model")

	// Run as user A — getOrCreateSession should create the session under userA.
	if _, err := RunAgent(ctx, mdl, nil, "inst", []*genai.Part{{Text: "hello from A"}}, svc, userA, sessID, nil); err != nil {
		t.Fatalf("RunAgent userA: %v", err)
	}

	aResp, err := svc.Get(ctx, &session.GetRequest{AppName: AppName, UserID: userA, SessionID: sessID})
	if err != nil {
		t.Fatalf("get userA session: %v", err)
	}
	aEvents := 0
	for range aResp.Session.Events().All() {
		aEvents++
	}
	if aEvents == 0 {
		t.Fatal("userA session has no events — RunAgent wrote to the wrong user")
	}

	// AppendStateEvent under userA must land on userA's session.
	if err := AppendStateEvent(ctx, svc, userA, sessID, map[string]any{"title": "A's title"}); err != nil {
		t.Fatalf("AppendStateEvent userA: %v", err)
	}
	aResp2, err := svc.Get(ctx, &session.GetRequest{AppName: AppName, UserID: userA, SessionID: sessID})
	if err != nil {
		t.Fatalf("re-get userA session: %v", err)
	}
	if v, _ := aResp2.Session.State().Get("title"); v != "A's title" {
		t.Errorf("userA title = %v, want 'A's title' — AppendStateEvent landed on the wrong user", v)
	}

	// Run as user B with the SAME sessionID — must be an isolated session.
	if _, err := RunAgent(ctx, mdl, nil, "inst", []*genai.Part{{Text: "hello from B"}}, svc, userB, sessID, nil); err != nil {
		t.Fatalf("RunAgent userB: %v", err)
	}

	bResp, err := svc.Get(ctx, &session.GetRequest{AppName: AppName, UserID: userB, SessionID: sessID})
	if err != nil {
		t.Fatalf("get userB session: %v", err)
	}
	bEvents := 0
	for range bResp.Session.Events().All() {
		bEvents++
	}
	if bEvents == 0 {
		t.Fatal("userB session has no events — RunAgent isn't isolating per user")
	}
	if v, _ := bResp.Session.State().Get("title"); v != nil {
		t.Errorf("userB session leaked state from userA: title = %v", v)
	}

	// DeleteSession under userB must not affect userA's session.
	DeleteSession(ctx, svc, userB, sessID)
	if _, err := svc.Get(ctx, &session.GetRequest{AppName: AppName, UserID: userA, SessionID: sessID}); err != nil {
		t.Errorf("userA session was deleted when userB called DeleteSession: %v", err)
	}
}
