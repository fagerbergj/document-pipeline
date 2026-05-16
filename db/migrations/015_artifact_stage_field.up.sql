ALTER TABLE artifacts ADD COLUMN stage TEXT;
ALTER TABLE artifacts ADD COLUMN field TEXT;

CREATE INDEX IF NOT EXISTS idx_artifacts_doc_stage_field
    ON artifacts(document_id, stage, field)
    WHERE stage IS NOT NULL AND field IS NOT NULL;
