package config

import (
	"fmt"
	"os"

	"github.com/fagerbergj/document-pipeline/server/core/model"
	"gopkg.in/yaml.v3"
)

// YAMLPipelineSource loads PipelineConfig from a YAML file, expanding ${VAR} env references.
type YAMLPipelineSource struct {
	Path string
}

var _ interface {
	Load() (model.PipelineConfig, error)
} = (*YAMLPipelineSource)(nil)

func (s *YAMLPipelineSource) Load() (model.PipelineConfig, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return model.PipelineConfig{}, fmt.Errorf("read pipeline config: %w", err)
	}

	expanded := os.Expand(string(data), func(key string) string {
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		return "${" + key + "}"
	})

	var raw struct {
		MaxConcurrent int `yaml:"max_concurrent"`
		Stages        []struct {
			Name           string              `yaml:"name"`
			Type           string              `yaml:"type"`
			Model          string              `yaml:"model"`
			Prompt         string              `yaml:"prompt"`
			Input          string              `yaml:"input"`
			Output         string              `yaml:"output"`
			Outputs        []model.StageOutput `yaml:"outputs"`
			RequireContext bool                `yaml:"require_context"`
			Destinations   []map[string]any    `yaml:"destinations"`
			MetadataFields []string            `yaml:"metadata_fields"`
			StartIf        map[string]any      `yaml:"start_if"`
			ContinueIf     []map[string]any    `yaml:"continue_if"`
			SkipIf         map[string]any      `yaml:"skip_if"`
			MaxConcurrent  *int                `yaml:"max_concurrent"`
			Vision         bool                `yaml:"vision"`
		} `yaml:"stages"`
	}

	if err := yaml.Unmarshal([]byte(expanded), &raw); err != nil {
		return model.PipelineConfig{}, fmt.Errorf("parse pipeline config: %w", err)
	}

	cfg := model.PipelineConfig{MaxConcurrent: raw.MaxConcurrent}
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
		})
	}

	return cfg, nil
}
