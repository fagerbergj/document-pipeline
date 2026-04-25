INSERT INTO jobs (id, document_id, stage, status, options, runs, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(document_id, stage) DO UPDATE SET
  status=excluded.status, options=excluded.options, runs=excluded.runs,
  updated_at=excluded.updated_at;
