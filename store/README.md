# store/

SQLite database schema. One database file, three tables.

The database file lives at `/data/pipeline.db` inside the container (bind-mounted from the host).

---

## Tables

### `documents`

One row per document. The two columns `current_stage` and `stage_state` form the state machine — never collapsed into a single combined string.

```sql
CREATE TABLE documents (
    id TEXT PRIMARY KEY,                          -- UUID
    content_hash TEXT UNIQUE NOT NULL,            -- SHA-256 hex of the PNG bytes
    created_at TEXT NOT NULL,                     -- ISO-8601 UTC
    updated_at TEXT NOT NULL,                     -- ISO-8601 UTC
    current_stage TEXT NOT NULL,                  -- stage name, 'done', or 'deleted'
    stage_state TEXT NOT NULL,                    -- 'pending'|'running'|'waiting'|'error'|'done'
    title TEXT,                                   -- set post-OCR
    date_month TEXT,                              -- 'YYYY-MM', set on receipt
    png_path TEXT,                                -- absolute path to PNG on disk
    duplicate_of TEXT REFERENCES documents(id),  -- set if duplicate_review resolved to keep_both
    stage_data TEXT NOT NULL DEFAULT '{}'         -- JSON: keyed by stage name, free-form
);

CREATE INDEX idx_documents_stage ON documents(current_stage, stage_state);
CREATE INDEX idx_documents_hash ON documents(content_hash);
```

**`stage_data` shape** (grows as pipeline advances):

```json
{
  "ocr": {
    "ocr_raw": "The quick brown fox..."
  },
  "clarify": {
    "clarified_text": "The quick brown fox...",
    "clarification_requests": [
      {"segment": "qu??k", "question": "Did you write 'quick' or 'quiet'?"}
    ],
    "clarification_responses": [
      {"segment": "qu??k", "answer": "quick"}
    ]
  },
  "classify": {
    "tags": ["productivity", "notes"],
    "summary": "A note about..."
  }
}
```

---

### `stage_events`

Append-only audit log. Never updated, only inserted. Retry count is derived by counting `failed` events for a given `(document_id, stage)` pair — no retry column needed.

```sql
CREATE TABLE stage_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    document_id TEXT NOT NULL REFERENCES documents(id),
    timestamp TEXT NOT NULL,         -- ISO-8601 UTC
    stage TEXT NOT NULL,             -- stage name (matches pipeline.yaml)
    event_type TEXT NOT NULL,        -- see values below
    data TEXT                        -- optional JSON payload
);

CREATE INDEX idx_events_document ON stage_events(document_id, stage, event_type);
```

**`event_type` values:**

| Value | When |
|---|---|
| `received` | Webhook receipt (document created) |
| `started` | Worker begins processing a stage |
| `completed` | Stage succeeded |
| `failed` | Stage raised an exception (may retry) |
| `reviewed` | User approved in review UI |
| `rejected` | User rejected (resets to previous stage) |
| `reprocess` | User triggered reprocess-from-stage |
| `deleted` | User triggered delete |

---

### `document_destinations`

Tracks external IDs per document per sink. Used to update or delete from sinks on re-processing or deletion.

```sql
CREATE TABLE document_destinations (
    document_id TEXT NOT NULL REFERENCES documents(id),
    destination_type TEXT NOT NULL,  -- e.g. 'qdrant'
    external_id TEXT NOT NULL,       -- e.g. Qdrant point UUID
    synced_at TEXT NOT NULL,         -- ISO-8601 UTC of last upsert
    PRIMARY KEY (document_id, destination_type)
);
```

---

## State machine values

`current_stage` + `stage_state` combinations that appear in normal operation:

| current_stage | stage_state | Meaning |
|---|---|---|
| `ocr` | `pending` | Waiting for OCR worker to pick up |
| `ocr` | `running` | OCR in progress |
| `ocr` | `error` | OCR failed 3 times, needs manual intervention |
| `clarify` | `pending` | Waiting for clarify worker |
| `clarify` | `running` | Clarify in progress |
| `clarify` | `error` | Clarify failed 3 times |
| `review_clarify` | `waiting` | Paused at manual review gate |
| `classify` | `pending` | Waiting for classify worker |
| `classify` | `running` | Classify in progress |
| `classify` | `error` | Classify failed 3 times |
| `review_classify` | `waiting` | Paused at manual review gate |
| `embed` | `pending` | Waiting for embed worker |
| `embed` | `running` | Embed in progress |
| `embed` | `error` | Embed failed 3 times |
| `duplicate_review` | `waiting` | Post-OCR title collision detected, needs resolution |
| `done` | `done` | Fully processed and indexed |
| `deleted` | `done` | Soft-deleted (not shown in UI by default) |

---

## Key query patterns

**Worker fetch** — all pending documents for a given stage, ordered by created_at:
```sql
SELECT * FROM documents
WHERE current_stage = ? AND stage_state = 'pending'
ORDER BY created_at ASC;
```

**Retry count** — how many times a stage has failed for a document:
```sql
SELECT COUNT(*) FROM stage_events
WHERE document_id = ? AND stage = ? AND event_type = 'failed';
```

**Review inbox** — all documents waiting for human review:
```sql
SELECT * FROM documents
WHERE stage_state = 'waiting'
AND current_stage NOT IN ('deleted')
ORDER BY updated_at ASC;
```

**Duplicate detection** — find existing document with same title:
```sql
SELECT id FROM documents
WHERE title = ? AND current_stage != 'deleted' AND id != ?
LIMIT 1;
```

**Diff data for review UI** — fetch stage_data snapshots before and after a stage:
Stage data is stored cumulatively in `documents.stage_data`. To show a diff, compare `stage_data[prev_stage]` with `stage_data[current_stage]`.

**Error list** — all documents in error state:
```sql
SELECT * FROM documents
WHERE stage_state = 'error'
ORDER BY updated_at DESC;
```

---

## Duplicate detection flow

1. OCR stage completes → worker extracts `title` from `stage_data.ocr.ocr_raw`
2. Query for existing document with same normalized title (case-insensitive)
3. If collision found:
   - Set `current_stage='duplicate_review', stage_state='waiting'`
   - Store `stage_data.duplicate_review.existing_id = <existing doc id>`
4. Duplicate review UI presents both documents side-by-side with three options:
   - **Keep both** — advance current doc to clarify stage, leave existing untouched
   - **Replace existing** — soft-delete existing (+ remove from Qdrant), advance current
   - **Discard** — soft-delete current doc
