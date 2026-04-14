package config

import (
	"os"
	"path/filepath"
	"testing"
)

const testPipelineYAML = `
max_concurrent: 2
stages:
  - name: ocr
    type: computer_vision
    model: ${TEST_MODEL}
    prompt: prompts/ocr.txt
    require_context: false
    skip_if:
      file_type: [txt, md]
    outputs:
      - field: ocr_raw
        type: text

  - name: embed
    type: embed
    model: nomic-embed-text
    input: ocr_raw
    metadata_fields: [title, tags]
`

func TestYAMLPipelineSource_Load(t *testing.T) {
	os.Setenv("TEST_MODEL", "llava")
	defer os.Unsetenv("TEST_MODEL")

	dir := t.TempDir()
	path := filepath.Join(dir, "pipeline.yaml")
	if err := os.WriteFile(path, []byte(testPipelineYAML), 0644); err != nil {
		t.Fatal(err)
	}

	src := &YAMLPipelineSource{Path: path}
	cfg, err := src.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.MaxConcurrent != 2 {
		t.Errorf("MaxConcurrent: got %d, want 2", cfg.MaxConcurrent)
	}
	if len(cfg.Stages) != 2 {
		t.Fatalf("Stages: got %d, want 2", len(cfg.Stages))
	}

	ocr := cfg.Stages[0]
	if ocr.Model != "llava" {
		t.Errorf("stage[0].Model: got %q (env var not expanded?)", ocr.Model)
	}
	if ocr.SkipIf == nil {
		t.Error("stage[0].SkipIf should not be nil")
	}

	embed := cfg.Stages[1]
	if len(embed.MetadataFields) != 2 {
		t.Errorf("stage[1].MetadataFields: got %v", embed.MetadataFields)
	}
}

func TestYAMLPipelineSource_DefaultMaxConcurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipeline.yaml")
	if err := os.WriteFile(path, []byte("stages: []"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := (&YAMLPipelineSource{Path: path}).Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConcurrent != 1 {
		t.Errorf("default MaxConcurrent: got %d, want 1", cfg.MaxConcurrent)
	}
}
