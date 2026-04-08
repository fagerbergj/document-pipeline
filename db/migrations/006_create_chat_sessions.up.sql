CREATE TABLE IF NOT EXISTS chat_sessions (
    id            TEXT PRIMARY KEY,
    title         TEXT NOT NULL DEFAULT '',
    system_prompt TEXT NOT NULL DEFAULT '',
    rag_retrieval TEXT NOT NULL DEFAULT '{"enabled": true, "max_sources": 5, "minimum_score": 0.0}',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
