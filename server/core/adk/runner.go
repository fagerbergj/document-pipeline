package adk

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/toolconfirmation"
	"google.golang.org/genai"

	"github.com/fagerbergj/document-pipeline/server/core/port"
)

// AppName is the fixed ADK session coordinate used throughout the pipeline.
// PipelineUserID is the UserID for pipeline sessions, configured via
// PIPELINE_SYSTEM_USER_ID env var with fallback to "pipeline" for local dev.
const AppName = "document-pipeline"

var PipelineUserID = cmp.Or(os.Getenv("PIPELINE_SYSTEM_USER_ID"), "pipeline")

// RunResult holds the final text and any tool responses accumulated during the
// agent loop.
type RunResult struct {
	Text          string
	ToolResponses []map[string]any
}

// StreamEventKind discriminates StreamEvent variants.
type StreamEventKind string

const (
	// StreamEventToken is a chunk of model-generated text. Concatenate to
	// rebuild the model's natural output.
	StreamEventToken StreamEventKind = "token"
	// StreamEventThinking is a chunk of out-of-band reasoning (e.g. qwen3's
	// `thinking` field). Surfaced separately from token so the client can
	// render it as a collapsible reasoning trace without parsing the content
	// stream.
	StreamEventThinking StreamEventKind = "thinking"
	// StreamEventToolCall fires when the model dispatches a tool. Args
	// carries the model-supplied arguments.
	StreamEventToolCall StreamEventKind = "tool_call"
	// StreamEventToolResult fires when a tool returns. Result carries the
	// tool's response payload.
	StreamEventToolResult StreamEventKind = "tool_result"
	// StreamEventConfirmationRequest fires when a tool calls
	// ctx.RequestConfirmation. The agent loop pauses; the client must POST a
	// decision back to /chats/{id}/confirmations/{call_id} to resume.
	StreamEventConfirmationRequest StreamEventKind = "confirmation_request"
)

// StreamEvent is a single discrete event emitted during an agent loop. Only
// the fields relevant to the Kind are populated.
type StreamEvent struct {
	Kind     StreamEventKind
	Text     string         // StreamEventToken
	ToolName string         // tool_call / tool_result / confirmation_request
	ToolArgs map[string]any // tool_call
	Result   map[string]any // tool_result
	CallID   string         // confirmation_request
	Hint     string         // confirmation_request
	Payload  any            // confirmation_request (the arbitrary payload the tool attached)
}

// JSONPayload returns the JSON object that should land in the SSE event's
// data: field. Centralizes the wire format so chat and worker stay aligned.
func (e StreamEvent) JSONPayload() ([]byte, error) {
	switch e.Kind {
	case StreamEventToken, StreamEventThinking:
		return json.Marshal(map[string]string{"text": e.Text})
	case StreamEventToolCall:
		return json.Marshal(map[string]any{"name": e.ToolName, "args": e.ToolArgs})
	case StreamEventToolResult:
		return json.Marshal(map[string]any{"name": e.ToolName, "result": e.Result})
	case StreamEventConfirmationRequest:
		return json.Marshal(map[string]any{
			"call_id":   e.CallID,
			"tool_name": e.ToolName,
			"hint":      e.Hint,
			"payload":   e.Payload,
		})
	}
	return []byte("{}"), nil
}

// SSEEventType returns the SSE `event:` type name for this event kind.
func (e StreamEvent) SSEEventType() string {
	switch e.Kind {
	case StreamEventToken:
		return port.EventToken
	case StreamEventThinking:
		return port.EventThinking
	case StreamEventToolCall:
		return port.EventToolCall
	case StreamEventToolResult:
		return port.EventToolResult
	case StreamEventConfirmationRequest:
		return port.EventConfirmationRequest
	}
	return ""
}

// RunAgent runs an ADK agent loop against a persistent session identified by
// sessionID. The session is created if it does not already exist.
//
// sessionSvc must be a database-backed session.Service so sessions persist
// across calls. Each call to RunAgent appends new events to the session,
// giving the model full conversation history without any manual replay.
// onEvent is called with each streamed StreamEvent as it becomes available —
// model tokens, tool calls, and tool results, in the order they happen. Pass
// nil to discard intermediate output.
func RunAgent(
	ctx context.Context,
	mdl adkmodel.LLM,
	tools []tool.Tool,
	instruction string,
	userParts []*genai.Part,
	sessionSvc session.Service,
	sessionID string,
	onEvent func(StreamEvent),
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

	// userParts == nil signals a resume call: the user's decision has already
	// been persisted via session.AppendEvent; the runner picks it up from
	// session state and re-runs the tool. Pass nil so we don't append an
	// empty "user" message in front of the resume.
	var userMsg *genai.Content
	if len(userParts) > 0 {
		userMsg = &genai.Content{Role: "user", Parts: userParts}
	}
	runCfg := agent.RunConfig{StreamingMode: agent.StreamingModeNone}

	var (
		finalText     strings.Builder
		toolResponses []map[string]any
	)

	for event, err := range r.Run(ctx, PipelineUserID, sessionID, userMsg, runCfg) {
		if err != nil {
			return RunResult{}, fmt.Errorf("adk run: %w", err)
		}
		if event == nil || event.Content == nil {
			continue
		}
		for _, p := range event.Content.Parts {
			if p.FunctionCall != nil && onEvent != nil {
				if p.FunctionCall.Name == toolconfirmation.FunctionCallName {
					ev := buildConfirmationRequest(p.FunctionCall)
					onEvent(ev)
				} else {
					onEvent(StreamEvent{
						Kind:     StreamEventToolCall,
						ToolName: p.FunctionCall.Name,
						ToolArgs: p.FunctionCall.Args,
					})
				}
			}
			if p.FunctionResponse != nil && p.FunctionResponse.Response != nil {
				toolResponses = append(toolResponses, p.FunctionResponse.Response)
				if onEvent != nil && p.FunctionResponse.Name != toolconfirmation.FunctionCallName {
					onEvent(StreamEvent{
						Kind:     StreamEventToolResult,
						ToolName: p.FunctionResponse.Name,
						Result:   p.FunctionResponse.Response,
					})
				}
			}
			if p.Text != "" && p.Thought {
				if onEvent != nil {
					onEvent(StreamEvent{Kind: StreamEventThinking, Text: p.Text})
				}
			} else if event.IsFinalResponse() && p.Text != "" {
				finalText.WriteString(p.Text)
				if onEvent != nil {
					onEvent(StreamEvent{Kind: StreamEventToken, Text: p.Text})
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
		UserID:    PipelineUserID,
		SessionID: sessionID,
	})
	if err == nil {
		return resp.Session, nil
	}
	// Session likely already exists — fall through to Get.
	getResp, getErr := svc.Get(ctx, &session.GetRequest{
		AppName:   AppName,
		UserID:    PipelineUserID,
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
		UserID:    PipelineUserID,
		SessionID: sessionID,
	})
}

// AppendStateEvent appends a metadata-only event to an existing session,
// applying stateDelta to the persistent session state.
func AppendStateEvent(ctx context.Context, svc session.Service, sessionID string, stateDelta map[string]any) error {
	getResp, err := svc.Get(ctx, &session.GetRequest{
		AppName:   AppName,
		UserID:    PipelineUserID,
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

// buildConfirmationRequest unpacks the args of an adk_request_confirmation
// FunctionCall into a StreamEvent the client can render as an approval card.
func buildConfirmationRequest(fc *genai.FunctionCall) StreamEvent {
	ev := StreamEvent{Kind: StreamEventConfirmationRequest, CallID: fc.ID}
	// ADK puts the confirmation under args["toolConfirmation"] as a
	// toolconfirmation.ToolConfirmation struct on the freshly emitted in-memory
	// event, but as a map[string]any once that event has round-tripped through
	// JSON session persistence. Handle both forms — asserting only map[string]any
	// (the previous behavior) silently dropped the hint and payload on the
	// in-memory path, so the approval card rendered an empty before/after diff
	// and an empty proposed-content box even though the edit applied on approve.
	switch tc := fc.Args["toolConfirmation"].(type) {
	case toolconfirmation.ToolConfirmation:
		ev.Hint, ev.Payload = tc.Hint, tc.Payload
	case *toolconfirmation.ToolConfirmation:
		ev.Hint, ev.Payload = tc.Hint, tc.Payload
	case map[string]any:
		if h, ok := tc["hint"].(string); ok {
			ev.Hint = h
		}
		ev.Payload = tc["payload"]
	}
	if orig, err := toolconfirmation.OriginalCallFrom(fc); err == nil && orig != nil {
		ev.ToolName = orig.Name
	}
	return ev
}

// RequestedConfirmationPayload returns the payload a tool attached when it
// called ctx.RequestConfirmation, by locating the pending
// adk_request_confirmation FunctionCall with the given callID in the session.
// Returns (nil, false) if not found or the payload is not an object.
//
// ADK builds the resumed ToolConfirmation solely from the user's
// FunctionResponse and does NOT merge the original request payload, so callers
// that need request-time context (e.g. the resolved stage/field the user
// actually reviewed) must recover it here and echo it back into the response.
func RequestedConfirmationPayload(sess session.Session, callID string) (map[string]any, bool) {
	if sess == nil {
		return nil, false
	}
	for e := range sess.Events().All() {
		if e == nil || e.Content == nil {
			continue
		}
		for _, p := range e.Content.Parts {
			fc := p.FunctionCall
			if fc == nil || fc.Name != toolconfirmation.FunctionCallName || fc.ID != callID {
				continue
			}
			if m, ok := buildConfirmationRequest(fc).Payload.(map[string]any); ok {
				return m, true
			}
			return nil, false
		}
	}
	return nil, false
}

// AppendConfirmationResponse persists the user's decision on a pending tool
// confirmation back into the session. The next RunAgent call (with no new
// user message) will detect the response and re-invoke the original tool
// with ctx.ToolConfirmation().Confirmed set.
//
// confirmed=true approves; payload may carry overrides (e.g. user-edited
// content) the tool reads via ctx.ToolConfirmation().Payload.
func AppendConfirmationResponse(ctx context.Context, svc session.Service, sessionID, callID string, confirmed bool, payload map[string]any) error {
	getResp, err := svc.Get(ctx, &session.GetRequest{
		AppName:   AppName,
		UserID:    PipelineUserID,
		SessionID: sessionID,
	})
	if err != nil {
		return fmt.Errorf("session get: %w", err)
	}
	resp := map[string]any{"confirmed": confirmed}
	if payload != nil {
		resp["payload"] = payload
	}
	e := session.NewEvent(uuid.NewString())
	e.Author = "user"
	e.Content = &genai.Content{
		Role: "user",
		Parts: []*genai.Part{{
			FunctionResponse: &genai.FunctionResponse{
				ID:       callID,
				Name:     toolconfirmation.FunctionCallName,
				Response: resp,
			},
		}},
	}
	return svc.AppendEvent(ctx, getResp.Session, e)
}
