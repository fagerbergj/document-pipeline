INSERT INTO jobs (id, document_id, stage, status, options, runs, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (document_id, stage) DO UPDATE SET
  status=EXCLUDED.status, options=EXCLUDED.options, runs=EXCLUDED.runs,
  updated_at=EXCLUDED.updated_at;
