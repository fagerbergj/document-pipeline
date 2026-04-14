SELECT * FROM chat_sessions
WHERE (created_at, id) < (?, ?)
ORDER BY created_at DESC, id DESC LIMIT ?;
