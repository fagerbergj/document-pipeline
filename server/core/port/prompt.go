package port

// PromptRenderer renders a named prompt template with the given data.
// Implemented by store/prompts.FilePromptRenderer.
type PromptRenderer interface {
	Render(path string, data any) (string, error)
}
