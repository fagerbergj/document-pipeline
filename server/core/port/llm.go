package port

import "context"

// LLMInference drives vision, text generation, embedding, and model unloading.
// Implemented by store/ollama.
type LLMInference interface {
	GenerateVision(ctx context.Context, model, prompt string, imageBytes []byte, onChunk func(string)) error
	GenerateText(ctx context.Context, model, prompt string, onChunk func(string)) error
	ChatStream(ctx context.Context, model string, messages []LLMMessage, onChunk func(string)) error
	GenerateEmbed(ctx context.Context, model, text string) ([]float32, error)
	Unload(ctx context.Context, model string) error
}

// LLMMessage is a single turn in a chat-style LLM call.
type LLMMessage struct {
	Role    string
	Content string
}
