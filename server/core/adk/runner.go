package adk

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// RunResult holds the final text and any tool responses accumulated during the
// agent loop.
type RunResult struct {
	Text          string
	ToolResponses []map[string]any
}

// RunAgent runs a single-turn ADK agent and returns the final text response
// plus any tool call results collected during the loop.
//
// history contains prior conversation turns (user/assistant roles only) which
// are injected as real session events so the model sees them as proper dialogue
// context rather than system-prompt text.
func RunAgent(
	ctx context.Context,
	mdl adkmodel.LLM,
	tools []tool.Tool,
	instruction string,
	userParts []*genai.Part,
	history []port.LLMMessage,
) (RunResult, error) {
	ag, err := llmagent.New(llmagent.Config{
		Name:        "pipeline_agent",
		Description: "Document pipeline agent",
		Model:       mdl,
		Instruction: instruction,
		Tools:       tools,
	})
	if err != nil {
		return RunResult{}, fmt.Errorf("adk agent: %w", err)
	}

	sessionSvc := session.InMemoryService()
	sessionID := uuid.NewString()

	createResp, err := sessionSvc.Create(ctx, &session.CreateRequest{
		AppName:   "document-pipeline",
		UserID:    "pipeline",
		SessionID: sessionID,
	})
	if err != nil {
		return RunResult{}, fmt.Errorf("adk session: %w", err)
	}
	sess := createResp.Session

	for _, msg := range history {
		role, author := msg.Role, msg.Role
		if msg.Role == "assistant" {
			role = "model"
			author = "pipeline_agent"
		} else if msg.Role != "user" {
			continue
		}
		e := session.NewEvent(uuid.NewString())
		e.Author = author
		e.LLMResponse = adkmodel.LLMResponse{
			Content: &genai.Content{
				Role:  role,
				Parts: []*genai.Part{{Text: msg.Content}},
			},
		}
		if err := sessionSvc.AppendEvent(ctx, sess, e); err != nil {
			return RunResult{}, fmt.Errorf("adk history append: %w", err)
		}
	}

	r, err := runner.New(runner.Config{
		AppName:           "document-pipeline",
		Agent:             ag,
		SessionService:    sessionSvc,
		AutoCreateSession: false,
	})
	if err != nil {
		return RunResult{}, fmt.Errorf("adk runner: %w", err)
	}

	userMsg := &genai.Content{Role: "user", Parts: userParts}
	runCfg := agent.RunConfig{StreamingMode: agent.StreamingModeNone}

	var (
		finalText     strings.Builder
		toolResponses []map[string]any
	)

	for event, err := range r.Run(ctx, "pipeline", sessionID, userMsg, runCfg) {
		if err != nil {
			return RunResult{}, fmt.Errorf("adk run: %w", err)
		}
		if event == nil || event.Content == nil {
			continue
		}
		for _, p := range event.Content.Parts {
			if p.FunctionResponse != nil && p.FunctionResponse.Response != nil {
				toolResponses = append(toolResponses, p.FunctionResponse.Response)
			}
			if event.IsFinalResponse() && p.Text != "" {
				finalText.WriteString(p.Text)
			}
		}
	}

	return RunResult{
		Text:          finalText.String(),
		ToolResponses: toolResponses,
	}, nil
}
