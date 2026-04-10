-- name: List
SELECT id, name, text, created_at FROM contexts ORDER BY created_at ASC;

-- name: Create
INSERT INTO contexts (id, name, text, created_at) VALUES (?, ?, ?, ?);

-- name: Get
SELECT id, name, text, created_at FROM contexts WHERE id=?;

-- name: Delete
DELETE FROM contexts WHERE id=?;
