CREATE TABLE IF NOT EXISTS documents (
    id                TEXT PRIMARY KEY,
    content_hash      TEXT UNIQUE NOT NULL,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    title             TEXT,
    date_month        TEXT,
    png_path          TEXT,
    duplicate_of      TEXT REFERENCES documents(id),
    additional_context TEXT NOT NULL DEFAULT '',
    linked_contexts   TEXT NOT NULL DEFAULT '[]'
);

CREATE INDEX IF NOT EXISTS idx_documents_hash ON documents(content_hash);
