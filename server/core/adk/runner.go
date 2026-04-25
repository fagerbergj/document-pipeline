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
)

// AppName and UserID are the fixed ADK session coordinates used throughout the
// pipeline. All session creates, gets, and deletes must use these values so
// sessions are addressable by the same key from any caller.
const (
	AppName = "document-pipeline"
	UserID  = "pipeline"
)

// RunResult holds the final text and any tool responses accumulated during the
// agent loop.
type RunResult struct {
	Text          string
	ToolResponses []map[string]any
}

// RunAgent runs an ADK agent loop against a persistent session identified by
// sessionID. The session is created if it does not already exist.
//
// sessionSvc must be a database-backed session.Service so sessions persist
// across calls. Each call to RunAgent appends new events to the session,
// giving the model full conversation history without any manual replay.
// RunAgent runs an ADK agent loop. onToken is called with each streamed chunk
// as it becomes available — tool-call announcements and final text. Pass nil to
// discard intermediate output.
func RunAgent(
	ctx context.Context,
	mdl adkmodel.LLM,
	tools []tool.Tool,
	instruction string,
	userParts []*genai.Part,
	sessionSvc session.Service,
	sessionID string,
	onToken func(string),
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

	sess, err := getOrCreateSession(ctx, sessionSvc, sessionID)
	if err != nil {
		return RunResult{}, err
	}
	_ = sess

	r, err := runner.New(runner.Config{
		AppName:           AppName,
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

	for event, err := range r.Run(ctx, UserID, sessionID, userMsg, runCfg) {
		if err != nil {
			return RunResult{}, fmt.Errorf("adk run: %w", err)
		}
		if event == nil || event.Content == nil {
			continue
		}
		for _, p := range event.Content.Parts {
			if p.FunctionCall != nil && onToken != nil {
				query := ""
				if q, ok := p.FunctionCall.Args["query"].(string); ok {
					query = q
				}
				onToken(fmt.Sprintf("*Searching: %q…*\n\n", query))
			}
			if p.FunctionResponse != nil && p.FunctionResponse.Response != nil {
				toolResponses = append(toolResponses, p.FunctionResponse.Response)
			}
			if event.IsFinalResponse() && p.Text != "" {
				finalText.WriteString(p.Text)
				if onToken != nil {
					onToken(p.Text)
				}
			}
		}
	}

	return RunResult{
		Text:          finalText.String(),
		ToolResponses: toolResponses,
	}, nil
}

// getOrCreateSession retrieves the session with sessionID, creating it if it
// does not exist yet.
func getOrCreateSession(ctx context.Context, svc session.Service, sessionID string) (session.Session, error) {
	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName:   AppName,
		UserID:    UserID,
		SessionID: sessionID,
	})
	if err == nil {
		return resp.Session, nil
	}
	// Session likely already exists — fall through to Get.
	getResp, getErr := svc.Get(ctx, &session.GetRequest{
		AppName:   AppName,
		UserID:    UserID,
		SessionID: sessionID,
	})
	if getErr != nil {
		return nil, fmt.Errorf("adk session create: %w; get: %w", err, getErr)
	}
	return getResp.Session, nil
}

// DeleteSession removes the persistent ADK session for a given ID.
// Safe to call when the session does not exist.
func DeleteSession(ctx context.Context, svc session.Service, sessionID string) {
	_ = svc.Delete(ctx, &session.DeleteRequest{
		AppName:   AppName,
		UserID:    UserID,
		SessionID: sessionID,
	})
}

// AppendStateEvent appends a metadata-only event to an existing session,
// applying stateDelta to the persistent session state.
func AppendStateEvent(ctx context.Context, svc session.Service, sessionID string, stateDelta map[string]any) error {
	getResp, err := svc.Get(ctx, &session.GetRequest{
		AppName:   AppName,
		UserID:    UserID,
		SessionID: sessionID,
	})
	if err != nil {
		return fmt.Errorf("session get: %w", err)
	}
	e := session.NewEvent(uuid.NewString())
	e.Author = "system"
	e.Actions.StateDelta = stateDelta
	return svc.AppendEvent(ctx, getResp.Session, e)
}
