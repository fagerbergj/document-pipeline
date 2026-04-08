# db/

SQLite database schema. One database file, eight tables.

The database file lives at `/data/pipeline.db` inside the container (bind-mounted from the host).

---

## Tables

### `documents`

One row per document. Stores source metadata only — processing state lives in `jobs`.

```sql
CREATE TABLE documents (
    id TEXT PRIMARY KEY,                          -- UUID
    content_hash TEXT UNIQUE NOT NULL,            -- SHA-256 hex of the file bytes
    created_at TEXT NOT NULL,                     -- ISO-8601 UTC
    updated_at TEXT NOT NULL,                     -- ISO-8601 UTC
    title TEXT,                                   -- set post-OCR (or from filename at upload)
    date_month TEXT,                              -- 'YYYY-MM', set on receipt
    png_path TEXT,                                -- absolute path to source image on disk
    duplicate_of TEXT REFERENCES documents(id),  -- set if resolved to keep_both
    additional_context TEXT NOT NULL DEFAULT '',  -- user-supplied per-document context
    linked_contexts TEXT NOT NULL DEFAULT '[]'   -- JSON array of context entry IDs
);

CREATE INDEX idx_documents_hash ON documents(content_hash);
```

---

### `jobs`

One row per pipeline stage per document. **Jobs are the primary processing unit** — status, runs, and options all live here.

```sql
CREATE TABLE jobs (
    id          TEXT PRIMARY KEY,                           -- UUID
    document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    stage       TEXT NOT NULL,                             -- stage name from pipeline.yaml
    status      TEXT NOT NULL DEFAULT 'pending',           -- pending|running|waiting|error|done
    options     TEXT NOT NULL DEFAULT '{}',                -- JSON: {require_context, embed: {embed_image}}
    runs        TEXT NOT NULL DEFAULT '[]',                -- JSON array of Run objects (see below)
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE(document_id, stage)
);

CREATE INDEX idx_jobs_document ON jobs(document_id, stage);
CREATE INDEX idx_jobs_status   ON jobs(status);
```

**`options` shape:**
```json
{
  "require_context": false,
  "embed": { "embed_image": false }
}
```

**`runs` shape** (appended on each LLM execution):
```json
[
  {
    "id": "uuid",
    "inputs":  [{"field": "ocr_raw", "text": "..."}],
    "outputs": [{"field": "clarified_text", "text": "..."}],
    "confidence": "high",
    "questions": [{"segment": "qu??k", "question": "Did you mean 'quick'?", "answer": "quick"}],
    "suggestions": {
      "additional_context": "",
      "linked_context": "",
      "linked_context_id": null
    },
    "created_at": "ISO-8601",
    "updated_at": "ISO-8601"
  }
]
```

**`status` values:**

| Value | Meaning |
|---|---|
| `pending` | Waiting for the worker to pick up |
| `running` | Worker is actively processing |
| `waiting` | Parked — needs context, low confidence, or human review |
| `error` | Failed 3 times; needs manual retry |
| `done` | Stage completed successfully |

**Valid status transitions:**

| From | To | How |
|---|---|---|
| `running` | `error` | Auto (3 failures) or `PUT /jobs/{id}/status` |
| `waiting` | `pending` | `PUT /jobs/{id}/status` (reject) |
| `waiting` | `done` | `PUT /jobs/{id}/status` (approve) |
| `error` | `pending` | `PUT /jobs/{id}/status` (retry) |
| `done` | `pending` | `PUT /jobs/{id}/status` (replay/cascade) |

---

### `artifacts`

Files associated with a document (source image, generated text, diagrams).

```sql
CREATE TABLE artifacts (
    id           TEXT PRIMARY KEY,
    document_id  TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    filename     TEXT NOT NULL,
    content_type TEXT NOT NULL,
    created_job_id TEXT,                          -- job that created this artifact (null = upload)
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE INDEX idx_artifacts_document ON artifacts(document_id);
```

Artifact files are stored at `<vault>/artifacts/<artifact_id>/<filename>`.

---

### `stage_events`

Append-only audit log. Never updated, only inserted.

```sql
CREATE TABLE stage_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    timestamp   TEXT NOT NULL,                    -- ISO-8601 UTC
    stage       TEXT NOT NULL,                    -- stage name or 'upload'/'webhook'
    event_type  TEXT NOT NULL,                    -- see values below
    data        TEXT                              -- optional JSON payload
);

CREATE INDEX idx_events_document ON stage_events(document_id, stage);
```

**`event_type` values:**

| Value | When |
|---|---|
| `received` | Document created (upload or webhook receipt) |
| `started` | Worker begins processing a stage |
| `completed` | Stage succeeded |
| `failed` | Stage raised an exception (may retry) |
| `status_done` | Job manually approved via API |
| `status_pending` | Job manually reset to pending via API |
| `status_error` | Job manually stopped via API |

Retry count is derived by counting `failed` events for a given `(document_id, stage)` pair — no retry column needed.

---

### `contexts`

Reusable context snippets that can be linked to documents.

```sql
CREATE TABLE contexts (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    text       TEXT NOT NULL,
    created_at TEXT NOT NULL
);
```

---

### `chat_sessions`

RAG-enabled chat conversation threads.

```sql
CREATE TABLE chat_sessions (
    id            TEXT PRIMARY KEY,
    title         TEXT NOT NULL DEFAULT '',
    system_prompt TEXT NOT NULL DEFAULT '',
    rag_retrieval TEXT NOT NULL DEFAULT '{"enabled": true, "max_sources": 5, "minimum_score": 0.0}',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
```

**`rag_retrieval` shape:**
```json
{
  "enabled": true,
  "max_sources": 5,
  "minimum_score": 0.0
}
```

---

### `chat_messages`

Individual messages within a chat session.

```sql
CREATE TABLE chat_messages (
    id          TEXT PRIMARY KEY,
    external_id TEXT,
    session_id  TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
    role        TEXT NOT NULL,                    -- 'user' | 'assistant' | 'system'
    content     TEXT NOT NULL,
    sources     TEXT,                             -- JSON array of source summaries (assistant only)
    created_at  TEXT NOT NULL
);

CREATE INDEX idx_chat_messages_session ON chat_messages(session_id, created_at ASC);
```

**`sources` shape** (assistant messages only, when RAG is enabled):
```json
[
  {
    "document_id": "uuid",
    "title": "My Note",
    "summary": "...",
    "date_month": "2026-01",
    "score": 0.847
  }
]
```

---

### `key_value`

General-purpose persistent key-value store.

```sql
CREATE TABLE key_value (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL               -- JSON
);
```

**Current keys:**

| Key pattern | Value |
|---|---|
| `ingest_meta:{doc_id}` | `{"meta": {...}, "attachment_filename": "..."}` — webhook/upload metadata for worker |

---

## Key query patterns

**Worker fetch** — all pending jobs for a given stage, ordered by creation time:
```sql
SELECT * FROM jobs
WHERE stage = ? AND status = 'pending'
ORDER BY created_at ASC;
```

**Retry count** — how many times a stage has failed for a document:
```sql
SELECT COUNT(*) FROM stage_events
WHERE document_id = ? AND stage = ? AND event_type = 'failed';
```

**Review inbox** — all jobs waiting for human review:
```sql
SELECT j.*, d.title FROM jobs j
JOIN documents d ON d.id = j.document_id
WHERE j.status = 'waiting'
ORDER BY j.updated_at ASC;
```

**Current job for a document** — most active job (priority: running > waiting > pending > error > done):
```sql
SELECT * FROM jobs WHERE document_id = ?
ORDER BY
  CASE status
    WHEN 'running' THEN 0
    WHEN 'waiting' THEN 1
    WHEN 'pending' THEN 2
    WHEN 'error'   THEN 3
    ELSE 4
  END,
  updated_at DESC
LIMIT 1;
```

**Paginated job list** (keyset, by created_at + id):
```sql
SELECT j.*, d.title FROM jobs j
JOIN documents d ON d.id = j.document_id
WHERE (j.created_at, j.id) > (?, ?)
ORDER BY j.created_at ASC, j.id ASC
LIMIT ?;
```

---

## Indexes

| Index | Columns | Purpose |
|---|---|---|
| `idx_documents_hash` | `documents(content_hash)` | Deduplication check on ingest |
| `idx_jobs_document` | `jobs(document_id, stage)` | Fast job lookup per document |
| `idx_jobs_status` | `jobs(status)` | Worker polling for pending jobs |
| `idx_artifacts_document` | `artifacts(document_id)` | Artifact list per document |
| `idx_events_document` | `stage_events(document_id, stage)` | Retry count and audit log |
| `idx_chat_messages_session` | `chat_messages(session_id, created_at ASC)` | Message history retrieval |
