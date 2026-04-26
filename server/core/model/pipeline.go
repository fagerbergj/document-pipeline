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
	ChunkSize      int `yaml:"chunk_size"`    // chars per chunk; 0 = default (1500)
	ChunkOverlap   int `yaml:"chunk_overlap"` // overlap chars; 0 = default (200)
	RequireContext bool
	StartIf        map[string]any
	ContinueIf     []map[string]any
	SkipIf         map[string]any
	Vision         bool
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
