-- name: Insert
INSERT INTO artifacts (id, document_id, filename, content_type, created_job_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: Get
SELECT * FROM artifacts WHERE document_id=? AND id=?;

-- name: ListForDocument
SELECT * FROM artifacts WHERE document_id=? ORDER BY created_at ASC;
