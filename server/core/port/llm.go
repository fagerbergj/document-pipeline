package port

import "context"

// LLMInference drives vision, text generation, embedding, and model unloading.
// Implemented by store/ollama.
type LLMInference interface {
	GenerateVision(ctx context.Context, model, prompt string, imageBytes []byte, onChunk func(string)) error
	GenerateText(ctx context.Context, model, prompt string, onChunk func(string)) error
	// ChatWithTools sends messages to the model with optional tool definitions.
	// Returns the response text and any tool calls the model requests.
	// If tool calls are returned, the caller should execute them and call again
	// with the results appended as tool-response messages.
	ChatWithTools(ctx context.Context, model string, messages []LLMMessage, tools []LLMTool) (string, []LLMToolCall, error)
	ChatStream(ctx context.Context, model string, messages []LLMMessage, onChunk func(string)) error
	GenerateEmbed(ctx context.Context, model, text string) ([]float32, error)
	Unload(ctx context.Context, model string) error
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
