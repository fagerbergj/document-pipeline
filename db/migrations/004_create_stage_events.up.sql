CREATE TABLE IF NOT EXISTS stage_events (
    id          SERIAL PRIMARY KEY,
    document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    timestamp   TEXT NOT NULL,
    stage       TEXT NOT NULL,
    event_type  TEXT NOT NULL,
    data        TEXT
);

CREATE INDEX IF NOT EXISTS idx_events_document ON stage_events(document_id, stage);
