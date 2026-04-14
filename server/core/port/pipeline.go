package port

import "github.com/fagerbergj/document-pipeline/server/core/model"

// PipelineSource loads the pipeline configuration.
// Implemented by store/config.YAMLPipelineSource.
type PipelineSource interface {
	Load() (model.PipelineConfig, error)
}
