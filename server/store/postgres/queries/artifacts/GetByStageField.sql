SELECT * FROM artifacts
WHERE document_id = $1 AND stage = $2 AND field = $3
ORDER BY created_at DESC
LIMIT 1;
