-- name: SessionCreate
INSERT INTO chat_sessions (id, title, system_prompt, rag_retrieval, created_at, updated_at)
VALUES (?, '', ?, ?, ?, ?);

-- name: SessionGet
SELECT * FROM chat_sessions WHERE id=?;

-- name: SessionDelete
DELETE FROM chat_sessions WHERE id=?;

-- name: SessionListNoFilter
SELECT * FROM chat_sessions ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: SessionListBefore
SELECT * FROM chat_sessions
WHERE (created_at, id) < (?, ?)
ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: SessionGetCreatedAt
SELECT created_at FROM chat_sessions WHERE id=?;

-- name: MessageAppend
INSERT INTO chat_messages (id, external_id, session_id, role, content, sources, created_at)
VALUES (?, NULL, ?, ?, ?, ?, ?);

-- name: MessageList
SELECT id, external_id, session_id, role, content, sources, created_at
FROM chat_messages WHERE session_id=? ORDER BY created_at ASC, id ASC;
