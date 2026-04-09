package core

import (
	"fmt"
	"os"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"gopkg.in/yaml.v3"
)

// LoadPipeline parses a pipeline YAML file, expanding ${VAR} references from env.
func LoadPipeline(path string) (model.PipelineConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return model.PipelineConfig{}, fmt.Errorf("read pipeline config: %w", err)
	}

	// Expand ${VAR} before YAML parsing so env vars substitute into all fields.
	expanded := os.Expand(string(data), func(key string) string {
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		return "${" + key + "}" // leave unexpanded if not set
	})

	var raw struct {
		MaxConcurrent int `yaml:"max_concurrent"`
		Stages        []struct {
			Name           string         `yaml:"name"`
			Type           string         `yaml:"type"`
			Model          string         `yaml:"model"`
			Prompt         string         `yaml:"prompt"`
			Input          string         `yaml:"input"`
			Output         string         `yaml:"output"`
			Outputs        []model.StageOutput `yaml:"outputs"`
			RequireContext bool           `yaml:"require_context"`
			Destinations   []map[string]any `yaml:"destinations"`
			MetadataFields []string       `yaml:"metadata_fields"`
			StartIf        map[string]any `yaml:"start_if"`
			ContinueIf     []map[string]any `yaml:"continue_if"`
			SkipIf         map[string]any `yaml:"skip_if"`
			MaxConcurrent  *int           `yaml:"max_concurrent"`
			Vision         bool           `yaml:"vision"`
			SaveAsArtifact bool           `yaml:"save_as_artifact"`
		} `yaml:"stages"`
	}

	if err := yaml.Unmarshal([]byte(expanded), &raw); err != nil {
		return model.PipelineConfig{}, fmt.Errorf("parse pipeline config: %w", err)
	}

	cfg := model.PipelineConfig{
		MaxConcurrent: raw.MaxConcurrent,
	}
	if cfg.MaxConcurrent == 0 {
		cfg.MaxConcurrent = 1
	}

	for _, s := range raw.Stages {
		cfg.Stages = append(cfg.Stages, model.StageDefinition{
			Name:           s.Name,
			Type:           s.Type,
			Model:          s.Model,
			Prompt:         s.Prompt,
			Input:          s.Input,
			Output:         s.Output,
			Outputs:        s.Outputs,
			RequireContext: s.RequireContext,
			Destinations:   s.Destinations,
			MetadataFields: s.MetadataFields,
			StartIf:        s.StartIf,
			ContinueIf:     s.ContinueIf,
			SkipIf:         s.SkipIf,
			MaxConcurrent:  s.MaxConcurrent,
			Vision:         s.Vision,
			SaveAsArtifact: s.SaveAsArtifact,
		})
	}

	return cfg, nil
}
