package model

// PipelineConfig is loaded from config/pipeline.yaml at startup.
type PipelineConfig struct {
	MaxConcurrent int
	Stages        []StageDefinition
}

type StageDefinition struct {
	Name           string
	Type           string // computer_vision|llm_text|embed
	Model          string
	Prompt         string
	Input          string
	Output         string
	Outputs        []StageOutput
	Destinations   []map[string]any
	MetadataFields []string
	RequireContext bool
	StartIf        map[string]any
	ContinueIf     []map[string]any
	SkipIf         map[string]any
	Vision         bool
	SaveAsArtifact bool
	MaxConcurrent  *int // nil means use PipelineConfig.MaxConcurrent
}

type StageOutput struct {
	Field string `yaml:"field"`
	Type  string `yaml:"type"`
}

const (
	StageTypeComputerVision = "computer_vision"
	StageTypeLLMText        = "llm_text"
	StageTypeEmbed          = "embed"
)
