SELECT id, external_id, session_id, role, content, sources, created_at
FROM chat_messages WHERE session_id=? ORDER BY created_at ASC, id ASC;
