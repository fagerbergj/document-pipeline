package prompts

import (
	"bytes"
	"fmt"
	"os"
	"text/template"
)

// FilePromptRenderer reads templates from disk and renders them with Go text/template.
type FilePromptRenderer struct{}

var _ interface {
	Render(string, any) (string, error)
} = (*FilePromptRenderer)(nil)

var funcMap = template.FuncMap{
	// inc returns i+1, used for 1-based loop indices in templates.
	"inc": func(i int) int { return i + 1 },
}

func (r *FilePromptRenderer) Render(path string, data any) (string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt %s: %w", path, err)
	}
	tmpl, err := template.New("prompt").Funcs(funcMap).Parse(string(src))
	if err != nil {
		return "", fmt.Errorf("parse prompt %s: %w", path, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render prompt %s: %w", path, err)
	}
	return buf.String(), nil
}
