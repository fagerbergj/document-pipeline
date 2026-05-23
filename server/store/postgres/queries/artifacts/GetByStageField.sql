-- Only user-seeded artifacts qualify (created_job_id IS NULL). Worker stage
-- outputs are also tagged with stage/field, so without this filter a re-run of a
-- stage would find its own previous output and treat it as a user seed.
SELECT * FROM artifacts
WHERE document_id = $1 AND stage = $2 AND field = $3 AND created_job_id IS NULL
ORDER BY created_at DESC
LIMIT 1;
