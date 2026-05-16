package prompts

import (
	"bytes"
	"fmt"
	"os"
	"sync"
	"text/template"
)

// FilePromptRenderer reads templates from disk and renders them with Go
// text/template. Parsed templates are cached by path; prompts are immutable
// in production, so a single parse-and-keep is correct. If a prompt file is
// edited during development, restart the server to pick up the change.
type FilePromptRenderer struct {
	cache sync.Map // path → *template.Template
}

var _ interface {
	Render(string, any) (string, error)
} = (*FilePromptRenderer)(nil)

var funcMap = template.FuncMap{
	// inc returns i+1, used for 1-based loop indices in templates.
	"inc": func(i int) int { return i + 1 },
}

func (r *FilePromptRenderer) Render(path string, data any) (string, error) {
	tmpl, err := r.load(path)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render prompt %s: %w", path, err)
	}
	return buf.String(), nil
}

func (r *FilePromptRenderer) load(path string) (*template.Template, error) {
	if cached, ok := r.cache.Load(path); ok {
		return cached.(*template.Template), nil
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read prompt %s: %w", path, err)
	}
	tmpl, err := template.New("prompt").Funcs(funcMap).Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("parse prompt %s: %w", path, err)
	}
	actual, _ := r.cache.LoadOrStore(path, tmpl)
	return actual.(*template.Template), nil
}
