package model

type RAGConfig struct {
	Enabled      bool    `json:"enabled"`
	MaxSources   int     `json:"max_sources"`
	MinimumScore float64 `json:"minimum_score"`
}
