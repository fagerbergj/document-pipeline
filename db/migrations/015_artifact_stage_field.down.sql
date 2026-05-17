DROP INDEX IF EXISTS idx_artifacts_doc_stage_field;
ALTER TABLE artifacts DROP COLUMN IF EXISTS field;
ALTER TABLE artifacts DROP COLUMN IF EXISTS stage;
