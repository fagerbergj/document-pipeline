CREATE TABLE IF NOT EXISTS jobs (
    id          TEXT PRIMARY KEY,
    document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    stage       TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending',
    options     TEXT NOT NULL DEFAULT '{}',
    runs        TEXT NOT NULL DEFAULT '[]',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE(document_id, stage)
);

CREATE INDEX IF NOT EXISTS idx_jobs_document ON jobs(document_id, stage);
CREATE INDEX IF NOT EXISTS idx_jobs_status   ON jobs(status);
