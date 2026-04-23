package model

type RAGConfig struct {
	Enabled      bool    `json:"enabled"`
	MaxSources   int     `json:"max_sources"`
	MinimumScore float64 `json:"minimum_score"`
}

type SourceRef struct {
	DocumentID string  `json:"document_id"`
	SeriesName string  `json:"series_name,omitempty"`
	Title      string  `json:"title"`
	Summary    string  `json:"summary"`
	Text       string  `json:"-"`
	DateMonth  string  `json:"date_month"`
	Score      float64 `json:"score"`
}
