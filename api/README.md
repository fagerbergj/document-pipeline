# api/

HTTP API reference. All endpoints are served by the same FastAPI process as the UI, on port `8000`.

---

## Webhook

### `POST /webhook`

Receives a document send from the reMarkable tablet via rmfakecloud integrations.

**This URL must not change.** rmfakecloud is configured to POST to `http://remarkable-bridge:8000/webhook` and cannot be reconfigured without accessing the tablet.

**Content-Type:** `multipart/form-data`

**Form fields:**

| Field | Type | Description |
|---|---|---|
| `data` | string (JSON) | Document metadata from rmfakecloud. Contains `destinations` (list of folder/title hints), `parent`, `folder`. |
| `attachment` | file (PNG) | Rendered image of the current sheet. |

**Response (200):**
```json
{"status": "ok"}
```

The webhook returns immediately after saving the PNG and creating the DB row. OCR and all subsequent stages run asynchronously.

**Response (422):** No image attachment found in the form.

**Response (415):** Content-Type is not `multipart/form-data`.

---

## Documents

### `GET /api/documents`

Returns all non-deleted documents, ordered by `created_at DESC`.

**Query parameters:**

| Param | Type | Default | Description |
|---|---|---|---|
| `stage` | string | — | Filter by `current_stage` |
| `state` | string | — | Filter by `stage_state` |
| `limit` | int | 50 | Max results |
| `offset` | int | 0 | Pagination offset |

**Response (200):**
```json
[
  {
    "id": "uuid",
    "title": "My Note",
    "current_stage": "review_clarify",
    "stage_state": "waiting",
    "created_at": "2024-01-15T10:30:00Z",
    "updated_at": "2024-01-15T10:31:05Z",
    "date_month": "2024-01",
    "png_path": "/mnt/personal01/remarkable/2024-01/a1b2c3d4_My_Note.png",
    "stage_data": { ... }
  }
]
```

---

## Review

### `POST /api/review/{id}/approve`

Approves a document at a `manual_review` stage. The document must have `stage_state='waiting'`.

**Request body (optional):**
```json
{
  "edits": {
    "clarified_text": "Updated text after review..."
  }
}
```

If `edits` is provided, field values are merged into `stage_data` before advancing.

**Response (200):** Updated document object (same shape as `GET /api/documents` item).

**Response (409):** Document is not in `waiting` state.

---

### `POST /api/review/{id}/reject`

Rejects a document at a `manual_review` stage. Resets the preceding non-review stage to `pending`.

**Request body (optional):**
```json
{
  "edits": {
    "clarified_text": "Corrected text..."
  }
}
```

**Response (200):** Updated document object.

---

### `POST /api/review/{id}/clarify`

Re-runs the preceding `llm_text` stage with clarification responses. Only valid when the stage had `clarifications: true` and `stage_data.<stage>` contains `clarification_requests`.

**Request body:**
```json
{
  "clarification_responses": [
    {"segment": "qu??k", "answer": "quick"},
    {"segment": "Jn 3:16", "answer": "John 3:16 (Bible reference)"}
  ]
}
```

**Response (200):** Updated document object.

---

## Duplicate review

### `POST /api/duplicate/{id}/resolve`

Resolves a duplicate review. The document must have `current_stage='duplicate_review', stage_state='waiting'`.

**Request body:**
```json
{
  "resolution": "keep_both"
}
```

`resolution` values: `keep_both` | `replace_existing` | `discard`

**Response (200):** Updated document object.

---

## Document actions

### `POST /api/documents/{id}/delete`

Soft-deletes a document. Sets `current_stage='deleted', stage_state='done'`. Also removes from Qdrant if a destination record exists.

**Response (200):** `{"status": "ok"}`

---

### `POST /api/documents/{id}/reprocess`

Resets a document to a given stage.

**Request body:**
```json
{
  "stage": "clarify"
}
```

**Response (200):** Updated document object.

**Response (400):** Stage name not found in pipeline config.

---

## Health

### `GET /healthz`

**Response (200):** `{"status": "ok"}`

---

## Phase 5 additions

### Qdrant MCP server (Claude Code)

After Phase 5, a Qdrant MCP server can be configured so Claude Code can query the `remarkable` collection directly.

Add to `~/.claude/settings.json`:
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

### OpenCode query config

OpenCode can query via the Open WebUI API (which reads Qdrant directly via `VECTOR_DB=qdrant`). Configure OpenCode with the Open WebUI base URL and API key from `.env`.
