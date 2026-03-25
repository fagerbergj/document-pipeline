# api/

HTTP API reference. All endpoints served by FastAPI on port `8000`.

The UI is a React SPA (`frontend/`) served at `/`. All data endpoints are under `/api/v1/`.

---

## Webhook

### `POST /webhook`

Receives a document from the reMarkable tablet via rmfakecloud.

**This URL must not change.** rmfakecloud is configured to POST to `http://remarkable-bridge:8000/webhook`.

**Content-Type:** `multipart/form-data`

| Field | Type | Description |
|---|---|---|
| `data` | string (JSON) | Document metadata from rmfakecloud |
| `attachment` | file (PNG) | Rendered image of the current sheet |

**Response (200):** `{"status": "ok"}`

The webhook returns immediately. OCR and all subsequent stages run asynchronously.

---

## Documents

### `GET /api/v1/documents`

List all non-deleted documents.

**Query parameters:**

| Param | Type | Default | Description |
|---|---|---|---|
| `stages` | string | — | Comma-separated stage names to filter by |
| `states` | string | — | Comma-separated state values to filter by |
| `sort` | string | `pipeline` | `pipeline` (stage ASC, created ASC) \| `created_desc` \| `created_asc` \| `title_asc` \| `title_desc` |

**Response:** array of document summary objects:
```json
[{
  "id": "uuid",
  "title": "string or null",
  "current_stage": "ocr",
  "stage_state": "pending|running|waiting|error|done",
  "created_at": "ISO-8601",
  "updated_at": "ISO-8601",
  "needs_context": false
}]
```

---

### `GET /api/v1/documents/{id}`

Full document detail including stage outputs, review payload, event log, and replay options.

**Response:**
```json
{
  "id": "uuid",
  "title": "string or null",
  "current_stage": "clarify",
  "stage_state": "waiting",
  "created_at": "ISO-8601",
  "updated_at": "ISO-8601",
  "document_context": "string",
  "context_required": false,
  "needs_context": false,
  "stage_displays": [{"name": "ocr", "fields": {"ocr_raw": "..."}}],
  "review": null,
  "replay_stages": [{"name": "ocr"}],
  "events": [{"timestamp": "ISO-8601", "stage": "ocr", "event_type": "completed", "data": null}]
}
```

`review` is non-null when `stage_state == "waiting"` and the current stage has LLM output:
```json
{
  "stage_name": "clarify",
  "input_field": "ocr_raw",
  "output_field": "clarified_text",
  "input_text": "...",
  "output_text": "...",
  "is_single_output": true,
  "confidence": "high|medium|low",
  "qa_rounds": 0,
  "clarification_requests": [{"segment": "...", "question": "..."}]
}
```

---

### `DELETE /api/v1/documents/{id}`

Permanently deletes a document and all its events and destinations.

**Response:** `{"ok": true}`

---

### `POST /api/v1/documents/{id}/title`

Update document title.

**Body:** `{"title": "new title"}`

**Response:** full document detail

---

### `POST /api/v1/documents/{id}/context`

Save document context without changing stage state.

**Body:** `{"document_context": "..."}`

**Response:** full document detail

---

### `POST /api/v1/documents/{id}/set-context`

Save document context and reset stage to `pending` so the worker picks it up.

**Body:** `{"document_context": "..."}`

**Response:** full document detail

---

### `POST /api/v1/documents/{id}/approve`

Advance a waiting document to the next stage. Optionally save edited output text first.

**Body:** `{"edited_text": ""}` (empty string = no edit)

**Response:** full document detail

---

### `POST /api/v1/documents/{id}/reject`

Reset current stage to `pending` so the worker re-runs it.

**Response:** full document detail

---

### `POST /api/v1/documents/{id}/clarify`

Append a Q&A round to `qa_history` and reset stage to `pending`.

**Body:**
```json
{
  "answers": {"0": "answer to first clarification", "1": "..."},
  "free_prompt": "optional additional instructions"
}
```

**Response:** full document detail

---

### `POST /api/v1/documents/{id}/stop`

Stop a running document (sets `stage_state='error'`).

**Response:** full document detail

---

### `POST /api/v1/documents/{id}/retry`

Reset an errored document back to `pending`.

**Response:** full document detail

---

### `POST /api/v1/documents/{id}/replay/{stage_name}`

Replay from a prior stage — clears all `stage_data` from that stage onward and resets to `pending`.

**Response:** full document detail

---

### `GET /api/v1/documents/{id}/stream`

SSE endpoint. Streams LLM tokens while a document is `running`, then emits a `done` event when the stage completes or the document state changes.

**Events:**
- `event: token` — `{"text": "..."}` — one chunk of LLM output
- `event: done` — `{}` — stage finished or state changed (client should reload)
- `: ping` — keepalive comment

---

## Counts

### `GET /api/v1/counts`

Returns document counts grouped by state and by stage.

**Response:**
```json
{
  "pending": 2,
  "running": 1,
  "waiting": 3,
  "error": 0,
  "done": 14,
  "by_stage": {
    "ocr": 1,
    "clarify": 4,
    "embed": 1
  }
}
```

---

## Pipeline

### `GET /api/v1/pipeline/stages`

Returns the ordered list of stage names from `pipeline.yaml`.

**Response:** `{"stages": ["ocr", "clarify", "classify", "embed"]}`

---

## Context library

### `GET /api/v1/context-library`

Returns all saved context entries.

**Response:** `[{"name": "...", "text": "..."}]`

---

### `POST /api/v1/context-library`

Add or update a context entry (matched by name).

**Body:** `{"name": "...", "text": "..."}`

**Response:** updated full list

---

### `DELETE /api/v1/context-library/{name}`

Delete a context entry by name.

**Response:** updated full list

---

## Qdrant MCP server (Claude Code)

Add to `~/.claude/settings.json` to query the `remarkable` collection from Claude Code:

```json
{
  "mcpServers": {
    "qdrant": {
      "command": "uvx",
      "args": ["mcp-server-qdrant"],
      "env": {
        "QDRANT_URL": "http://localhost:6333",
        "COLLECTION_NAME": "remarkable"
      }
    }
  }
}
```
