# api/

HTTP API reference. All endpoints served on port `8000` under `/api/v1/`.

The React SPA is served at `/`. The OpenAPI schema is at `/openapi.json`.

All list endpoints return cursor-based paginated responses: `{"data": [...], "next_page_token": "..."}`.

---

## Pipelines

| Method | Path | Description |
|---|---|---|
| `GET` | `/pipelines` | List pipelines (always returns one) |
| `GET` | `/pipelines/{id}` | Pipeline detail with stage specs, inputs, outputs, and conditions |

---

## Documents

Documents are metadata containers. Processing state lives in jobs.

| Method | Path | Description |
|---|---|---|
| `GET` | `/documents` | List documents — filterable by stage/status, sortable, paginated |
| `POST` | `/documents` | Upload a file (PNG/JPG/TXT/MD) — returns first pipeline job |
| `GET` | `/documents/{id}` | Document detail with artifacts, context, and current job |
| `PATCH` | `/documents/{id}` | Update title, additional context, or linked contexts |
| `DELETE` | `/documents/{id}` | Delete document (cascades to jobs, artifacts, events) |
| `GET` | `/documents/{id}/artifacts/{artifact_id}` | Download an artifact file |

---

## Jobs

Jobs are the primary processing unit — one per pipeline stage per document.

| Method | Path | Description |
|---|---|---|
| `GET` | `/jobs` | List jobs — filterable by id/document/stage/status, paginated |
| `GET` | `/jobs/{id}` | Job detail with options and full runs history |
| `PATCH` | `/jobs/{id}` | Update job options (require_context, embed_image) |
| `PATCH` | `/jobs/{id}/runs/{run_id}` | Answer clarification questions or accept suggestions |
| `PUT` | `/jobs/{id}/status` | Transition status: approve (waiting→done), reject (waiting→pending), retry (error→pending), replay (done→pending) |
| `GET` | `/jobs/{id}/stream` | SSE stream of LLM tokens while job is running |

---

## Contexts

| Method | Path | Description |
|---|---|---|
| `GET` | `/contexts` | List all context entries |
| `POST` | `/contexts` | Create a context entry |
| `PATCH` | `/contexts/{id}` | Update name or text |
| `DELETE` | `/contexts/{id}` | Delete a context entry |

---

## Chat

RAG-enabled chat backed by Qdrant vector search and Ollama.

| Method | Path | Description |
|---|---|---|
| `GET` | `/chats` | List sessions (cursor: `before_id`) |
| `POST` | `/chats` | Create session (system prompt + RAG config) |
| `GET` | `/chats/{id}` | Session with full message history |
| `PATCH` | `/chats/{id}` | Update title, system prompt, or RAG config |
| `DELETE` | `/chats/{id}` | Delete session |
| `POST` | `/chats/{id}/messages` | Send message — SSE stream: `sources` event then `token` events |
