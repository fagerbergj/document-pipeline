package model

import "time"

type ChatSession struct {
	ID           string
	Title        string
	SystemPrompt string
	RAGRetrieval RAGConfig
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type RAGConfig struct {
	Enabled      bool    `json:"enabled"`
	MaxSources   int     `json:"max_sources"`
	MinimumScore float64 `json:"minimum_score"`
}

type ChatMessage struct {
	ID         string
	ExternalID *string
	SessionID  string
	Role       string // user|assistant|system
	Content    string
	Sources    []SourceRef
	CreatedAt  time.Time
}

type SourceRef struct {
	DocumentID string  `json:"document_id"`
	Title      string  `json:"title"`
	Summary    string  `json:"summary"`
	Text       string  `json:"-"`
	DateMonth  string  `json:"date_month"`
	Score      float64 `json:"score"`
}
