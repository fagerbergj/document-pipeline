-- Pin search_path on the index-queue trigger functions so they resolve
-- index_queue regardless of the caller's session search_path. Without
-- this, ad-hoc psql sessions (e.g. shared-postgres without a custom
-- search_path) fail with "relation index_queue does not exist" when they
-- UPDATE documents or jobs.
--
-- FROM CURRENT captures the search_path active at migration time, so the
-- function works whether installed into the app schema (document_pipeline)
-- or a test schema (test_xxx) — each environment captures its own value.

ALTER FUNCTION fn_idx_doc_insert() SET search_path FROM CURRENT;
ALTER FUNCTION fn_idx_doc_update() SET search_path FROM CURRENT;
ALTER FUNCTION fn_idx_doc_delete() SET search_path FROM CURRENT;
ALTER FUNCTION fn_idx_job_insert() SET search_path FROM CURRENT;
ALTER FUNCTION fn_idx_job_update() SET search_path FROM CURRENT;
