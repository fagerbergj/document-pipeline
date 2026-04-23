package core

// OCRPromptData is the template data for the computer_vision stage.
type OCRPromptData struct {
	DocumentContext string
}

// ClarifyPromptData is the template data for the clarify llm_text stage.
type ClarifyPromptData struct {
	DocumentContext   string
	LinkedContext     string
	LinkedContextName string
}

// ClassifyPromptData is the template data for the classify llm_text stage.
type ClassifyPromptData struct {
	Context         string
	DocumentContext string
}
