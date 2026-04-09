package core

import (
	"bytes"
	"fmt"
	"os"
	"text/template"
)

var promptFuncs = template.FuncMap{
	// inc returns i+1, used for 1-based loop indices in templates.
	"inc": func(i int) int { return i + 1 },
}

// RenderPrompt reads the template file at path and executes it with data.
func RenderPrompt(path string, data any) (string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt %s: %w", path, err)
	}
	tmpl, err := template.New("prompt").Funcs(promptFuncs).Parse(string(src))
	if err != nil {
		return "", fmt.Errorf("parse prompt %s: %w", path, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render prompt %s: %w", path, err)
	}
	return buf.String(), nil
}

// OCRPromptData is the template data for the computer_vision stage.
type OCRPromptData struct {
	DocumentContext string
}

// ClarifyPromptData is the template data for the clarify llm_text stage.
type ClarifyPromptData struct {
	DocumentContext   string
	LinkedContext     string
	LinkedContextName string
	FreePrompt        string
	PreviousOutput    string
	QAHistory         []QARound
}

// QARound is one round of Q&A answers from a prior clarify run.
type QARound struct {
	Responses  []QAResponse
	FreePrompt string
}

// QAResponse is one answered clarification question.
type QAResponse struct {
	Segment string
	Answer  string
}

// ClassifyPromptData is the template data for the classify llm_text stage.
type ClassifyPromptData struct {
	Context         string
	DocumentContext string
}
