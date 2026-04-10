-- name: Upsert
INSERT INTO jobs (id, document_id, stage, status, options, runs, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(document_id, stage) DO UPDATE SET
  status=excluded.status, options=excluded.options, runs=excluded.runs,
  updated_at=excluded.updated_at;

-- name: GetByID
SELECT * FROM jobs WHERE id=?;

-- name: GetByDocumentAndStage
SELECT * FROM jobs WHERE document_id=? AND stage=?;

-- name: UpdateStatus
UPDATE jobs SET status=?, updated_at=? WHERE id=?;

-- name: UpdateOptions
UPDATE jobs SET options=?, updated_at=? WHERE id=?;

-- name: UpdateRuns
UPDATE jobs SET runs=?, updated_at=? WHERE id=?;

-- name: ListForDocument
SELECT * FROM jobs WHERE document_id=? ORDER BY created_at ASC;

-- name: ListPending
SELECT * FROM jobs WHERE stage=? AND status='pending' ORDER BY created_at ASC;

-- name: ResetRunning
UPDATE jobs SET status='pending' WHERE status='running';
