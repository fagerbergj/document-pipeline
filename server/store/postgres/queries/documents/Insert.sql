INSERT INTO documents (id, content_hash, created_at, updated_at, title, date_month, media_path, duplicate_of, additional_context, linked_contexts, series)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);
