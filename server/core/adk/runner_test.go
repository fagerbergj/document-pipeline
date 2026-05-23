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
