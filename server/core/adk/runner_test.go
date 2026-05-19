package adk

import (
	"context"
	"testing"

	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

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
