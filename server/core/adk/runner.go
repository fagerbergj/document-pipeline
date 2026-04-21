package adk

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// RunResult holds the final text and any tool responses accumulated during the
// agent loop.
type RunResult struct {
	Text          string
	ToolResponses []map[string]any
}

// RunAgent runs a single-turn ADK agent and returns the final text response
// plus any tool call results collected during the loop.
func RunAgent(
	ctx context.Context,
	mdl model.LLM,
	tools []tool.Tool,
	instruction string,
	userParts []*genai.Part,
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
	r, err := runner.New(runner.Config{
		AppName:           "document-pipeline",
		Agent:             ag,
		SessionService:    sessionSvc,
		AutoCreateSession: true,
	})
	if err != nil {
		return RunResult{}, fmt.Errorf("adk runner: %w", err)
	}

	// Each invocation gets a unique session ID so concurrent calls don't collide.
	sessionID := uuid.NewString()
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
