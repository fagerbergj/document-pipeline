-- name: Set
INSERT INTO key_value (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value;

-- name: Get
SELECT value FROM key_value WHERE key=?;
