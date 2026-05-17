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
	// ContextualModel, when set on an embed stage, enables contextual
	// embeddings: before embedding each chunk a small LLM is asked to produce
	// a 1-2 sentence situating context, which is prepended to the chunk text
	// fed into the embedding model. Empty disables (chunks are embedded raw).
	ContextualModel string
	// ContextualPrompt is the path to the prompt template used by the
	// contextual embedding step. Required when ContextualModel is set.
	ContextualPrompt string
}

type StageOutput struct {
	Field string `yaml:"field"`
	Type  string `yaml:"type"`
}

const (
	StageTypeComputerVision = "computer_vision"
	StageTypeLLMText        = "llm_text"
	StageTypeEmbed          = "embed"
	StageTypeTranscribe     = "transcribe"
)

// StageName* are the canonical names used in pipeline.yaml. They are
// referenced by code that needs to read a specific stage's outputs (e.g.
// chat tools fetching the clarify stage's clarified_text). Stage names are
// ultimately config-driven, so a rename in pipeline.yaml must be matched
// here — these constants exist to make such references findable in one
// place.
const (
	StageNameTranscribe = "transcribe"
	StageNameOCR        = "ocr"
	StageNameSummarize  = "summarize"
	StageNameClarify    = "clarify"
	StageNameClassify   = "classify"
	StageNameEmbed      = "embed"
)

// Output field names emitted by the standard pipeline stages.
const (
	FieldRawText          = "raw_text"
	FieldNarrativeSummary = "narrative_summary"
	FieldClarifiedText    = "clarified_text"
	FieldTags             = "tags"
	FieldSummary          = "summary"
)

// StageOutputs is a stage_name → field_name → output_value map for a single
// document. It is what core.CollectStageData returns and what most code
// consumes when reading completed-stage outputs.
type StageOutputs = map[string]map[string]any

// StageOutputsByDoc keys StageOutputs by document id. Returned by the batch
// helpers (core.CollectStageDataBatch) consumed by tools that resolve many
// docs at once.
type StageOutputsByDoc = map[string]StageOutputs
