-- Pin search_path on the index-queue trigger functions so they resolve
-- index_queue regardless of the caller's session search_path. Without
-- this, ad-hoc psql sessions (e.g. shared-postgres without a custom
-- search_path) fail with "relation index_queue does not exist" when they
-- UPDATE documents or jobs.

ALTER FUNCTION fn_idx_doc_insert() SET search_path = document_pipeline, public;
ALTER FUNCTION fn_idx_doc_update() SET search_path = document_pipeline, public;
ALTER FUNCTION fn_idx_doc_delete() SET search_path = document_pipeline, public;
ALTER FUNCTION fn_idx_job_insert() SET search_path = document_pipeline, public;
ALTER FUNCTION fn_idx_job_update() SET search_path = document_pipeline, public;
