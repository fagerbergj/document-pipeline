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

Returns the document table HTML partial for HTMX. All non-deleted documents.

**Query parameters:**

| Param | Type | Default | Description |
|---|---|---|---|
| `stage` | string | — | Filter by `current_stage` |
| `state` | string | — | Filter by `stage_state` |
| `sort` | string | `created_desc` | Sort order: `created_desc`, `created_asc`, `title_asc`, `title_desc` |

---

## Document detail and review actions

All review actions are submitted as HTML form POSTs from the document detail page (`/documents/{id}`) and redirect back to that page on success. They return HTML, not JSON.

### `GET /documents/{id}`

Document detail page. Also the review UI for parked documents.

---

### `POST /documents/{id}/approve`

Advances a parked document to the next stage. Accepts an optional `edited_text` form field; if provided and the current stage has a single-text `output`, the edited value is saved before advancing.

---

### `POST /documents/{id}/reject`

Resets the current stage to `pending` so the worker re-runs it.

---

### `POST /documents/{id}/clarify`

Appends a Q&A round to `stage_data.<stage>.qa_history` and resets the stage to `pending`. Form fields: one `answer_N` field per clarification request, plus an optional `free_prompt` field.

---

### `POST /documents/{id}/stop`

Stops a running document by setting `stage_state='error'`.

---

### `POST /documents/{id}/retry`

Resets an errored document back to `pending` on its current stage.

---

### `POST /documents/{id}/replay/{stage_name}`

Resets the document to replay from the given stage, clearing all downstream `stage_data` entries.

---

### `POST /documents/{id}/title`

Updates the document title. Form field: `title`.

---

### `POST /documents/{id}/context`

Saves document context without changing stage state. Form field: `document_context`.

---

### `POST /documents/{id}/set-context`

Saves document context and resets the stage to `pending` so the worker picks it up. Form field: `document_context`.

---

## HTMX partials

### `GET /api/documents`

Returns the document table partial (`partials/document_table.html`). Used by HTMX to refresh the table.

**Query parameters:** `stage`, `state`, `sort` — same filters as the dashboard.

---

### `GET /api/documents/{id}/stream`

SSE endpoint. Streams LLM tokens while a document is in `running` state, then emits a `done` event when the stage completes or the document state changes.

---

### `POST /api/documents/{id}/replay/{stage_name}`

HTMX variant of the replay action — returns the updated document table partial instead of redirecting.

---

### `POST /api/documents/{id}/stop`

HTMX variant of stop — returns the updated document table partial.

---

### `POST /api/documents/{id}/title`

HTMX variant of title update — returns the updated document table partial.

---

### `POST /api/documents/{id}/retry`

HTMX variant of retry — returns the updated document table partial.

---

## Context library

### `GET /contexts`

Context library management page.

### `GET /api/context-library`

Returns the context picker dropdown partial.

### `POST /api/context-library`

Adds or updates a context entry. Form fields: `library_name`, `library_text`.

### `POST /api/context-library/delete`

Deletes a context entry. Form field: `name`.

---

## Health

### `GET /healthz`

**Response (200):** `{"status": "ok"}`

---

## Qdrant MCP server (Claude Code)

A Qdrant MCP server can be configured so Claude Code can query the `remarkable` collection directly.

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
