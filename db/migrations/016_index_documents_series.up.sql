CREATE INDEX IF NOT EXISTS idx_documents_series ON documents (series) WHERE series IS NOT NULL AND series <> '';
