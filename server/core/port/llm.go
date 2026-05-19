package port

import "context"

// LLMInference drives vision, text generation, embedding, and chat. Model
// lifecycle (load / swap / unload) is handled upstream by the inference
// server (llama-swap routes per-model and unloads on its own TTL) — there is
// no Unload method here.
type LLMInference interface {
	GenerateVision(ctx context.Context, model, prompt string, imageBytes []byte, onChunk func(string)) error
	GenerateText(ctx context.Context, model, prompt string, onChunk func(string)) error
	// ChatWithTools sends messages to the model with optional tool definitions.
	// Returns the response text, any out-of-band reasoning the model emitted
	// (separate from the final answer), and any tool calls the model requests.
	// If tool calls are returned, the caller should execute them and call again
	// with the results appended as tool-response messages.
	ChatWithTools(ctx context.Context, model string, messages []LLMMessage, tools []LLMTool) (LLMChatResponse, error)
	ChatStream(ctx context.Context, model string, messages []LLMMessage, onChunk func(string)) error
	GenerateEmbed(ctx context.Context, model, text string) ([]float32, error)
}

// LLMMessage is a single turn in a chat-style LLM call.
// Role is one of: "system", "user", "assistant", "tool".
type LLMMessage struct {
	Role       string
	Content    string
	Images     [][]byte      // raw image bytes (user messages only)
	ToolCalls  []LLMToolCall // assistant messages requesting tool calls
	ToolCallID string        // tool messages: ID of the call being responded to
}

// LLMChatResponse is the result of a non-streaming chat call. Reasoning
// models (qwen3, etc.) may emit Thinking out-of-band; if present, it should
// be surfaced separately from Text rather than concatenated. Text may be
// non-empty alongside ToolCalls — callers should not assume mutual
// exclusion.
type LLMChatResponse struct {
	Text      string
	Thinking  string
	ToolCalls []LLMToolCall
}

// LLMTool describes a function the model may call.
type LLMTool struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON-schema object for the function parameters
}

// LLMToolCall is a single tool invocation requested by the model.
type LLMToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
}
