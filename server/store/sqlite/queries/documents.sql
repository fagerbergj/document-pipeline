-- name: Insert
INSERT INTO documents
  (id, content_hash, created_at, updated_at, title, date_month,
   png_path, duplicate_of, additional_context, linked_contexts)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: Get
SELECT * FROM documents WHERE id = ?;

-- name: GetByHash
SELECT * FROM documents WHERE content_hash = ?;

-- name: Update
UPDATE documents SET
  updated_at=?, title=?, date_month=?, png_path=?, duplicate_of=?,
  additional_context=?, linked_contexts=?
WHERE id=?;

-- name: Delete
DELETE FROM documents WHERE id=?;
