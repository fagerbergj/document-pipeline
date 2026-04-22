# document-pipeline

A config-driven, multi-stage document ingestion pipeline. Upload handwritten reMarkable notes or text/image files, run them through OCR, LLM cleanup, and classification, then index them in Qdrant (vector store) and OpenSearch (full-text) for RAG queries and Lucene search.

## What it does

```
Upload (PNG / JPG / TXT / MD)
  → OCR (computer vision model via Ollama)
  → Clarify (LLM cleanup + Q&A clarification loop)
      [parks for human review if confidence < high]
  → Classify (tags + summary)
      [parks for human review if confidence < high]
  → Embed → Qdrant + Open WebUI (vector stores) + OpenSearch (full-text)
```

All stages, models, prompts, and review gates are defined in [`config/pipeline.yaml`](config/README.md) — no code changes needed to add, remove, or reorder stages.

## How it works

### Jobs are the primary unit

Every document moves through the pipeline via **jobs** — one job per pipeline stage. Jobs track status (`pending` → `running` → `waiting` / `error` / `done`), store LLM inputs and outputs in structured `runs`, and are the target of all review actions. Documents are lightweight metadata containers.

### Human-in-the-loop review

When an LLM stage produces low-confidence output or raises clarification questions, the job parks at `waiting`. A human can:

- **Approve** — advance the job to the next stage (optionally with edited text)
- **Reject** — reset to `pending` so the worker re-runs
- **Clarify** — answer the LLM's questions and re-run with the answers appended
- **Replay** — rewind any completed job back to `pending` (cascades downstream)

### Search

The dashboard search box accepts Lucene queries. Documents are indexed into OpenSearch by a background `IndexerService` that polls an `index_queue` table populated by SQLite triggers on every document and job change. Searchable fields: `title`, `series`, `summary`, `tags`, `content`, `date_month`, `stage`, `status`.

Examples: `title:meeting`, `status:pending`, `tags:invoice AND summary:budget`, `content:"quarterly review"`.

### Chat / RAG

A built-in chat interface streams LLM responses with cited sources. Chat sessions persist with full message history. The LLM queries the knowledge base autonomously via the `rag_search` tool (see LLM loops below).

### Context library

Reusable context snippets can be attached to documents at ingest or during review. The worker injects linked contexts and per-document context into LLM prompts.

## Quickstart

### Prerequisites

- Docker + Docker Compose
- The `llm` stack from `home-server` running (Ollama + Open WebUI + Qdrant)

### Environment variables

Copy from `home-server/.env`. Required variables per pipeline phase:

| Variable | Phase | Description |
|---|---|---|
| `OLLAMA_URL` | 2 | Ollama endpoint (e.g. `http://ollama:11434`) |
| `OCR_MODEL` | 2 | Ollama model for OCR (e.g. `qwen3-vl:30b`) |
| `CLARIFY_MODEL` | 3 | Ollama model for clarification (e.g. `gemma4:31b`) |
| `CLASSIFY_MODEL` | 4 | Ollama model for classification (e.g. `gemma4-26b:latest`) |
| `EMBED_MODEL` | 5 | Ollama embedding model (e.g. `nomic-embed-text:v1.5`) |
| `QDRANT_URL` | 5 | Qdrant endpoint (e.g. `http://qdrant:6333`) |
| `QDRANT_COLLECTION` | 5 | Collection name (e.g. `remarkable`) |
| `QDRANT_API_KEY` | 5 | Optional Qdrant API key |
| `OPEN_WEBUI_URL` | 5 | Open WebUI base URL |
| `OPEN_WEBUI_API_KEY` | 5 | Open WebUI API key |
| `OPEN_WEBUI_KNOWLEDGE_ID` | 5 | Knowledge base ID in Open WebUI |
| `OPENSEARCH_URL` | 5 | OpenSearch endpoint (e.g. `http://opensearch:9200`) |
| `OPENSEARCH_INDEX` | 5 | OpenSearch index name (default `documents`) |

Optional:

| Variable | Default | Description |
|---|---|---|
| `DB_PATH` | `/data/pipeline.db` | SQLite database path |
| `VAULT_PATH` | `/vault` | Artifact storage directory |
| `MIGRATIONS_DIR` | `db/migrations` | SQL migration directory |
| `PIPELINE_CONFIG` | `config/pipeline.yaml` | Pipeline YAML path |
| `LISTEN_ADDR` | `:8000` | HTTP listen address |

### Run

```bash
docker compose up --build
```

The service listens on port 8000. The UI is at `http://localhost:8000`.

In production the service is deployed via `home-server/notes/docker-compose.yml` under the container name `document-pipeline`.

## Project structure

```
document-pipeline/
├── server/            Go service
│   ├── main.go        Entry point — flags, dependency wiring, graceful shutdown
│   ├── api/rest/      HTTP handlers (chi router)
│   ├── core/          Domain services (ingest, worker) + port interfaces + models
│   ├── store/         Outbound adapters: sqlite, ollama, qdrant, openwebui,
│   │                  opensearch, filesystem, stream, prompts, config, embed (coordinator)
│   ├── web/           Embedded frontend bundle (//go:embed all:dist)
│   └── test/          Integration tests
├── frontend/          React + Vite SPA (TypeScript + Tailwind + React Query)
├── config/            pipeline.yaml + reference docs
├── db/migrations/     SQL up/down migrations
└── prompts/           LLM prompt templates (plain text)
```

## Architecture

### Hexagonal, in Go

```
server/api/rest/  →  server/core/  →  server/store/
  (chi handlers)     (ingest,           (sqlite, ollama, qdrant,
                      worker)            openwebui, filesystem, ...)
                         ↑
                    server/core/port/
                    (interfaces only)
```

- **Core** (`server/core/`) contains all business logic. It depends only on the port
  interfaces in `server/core/port/` — no I/O imports.
- **Inbound adapter** (`server/api/rest/`) translates HTTP requests into core
  service calls using the go-chi router.
- **Outbound adapters** (`server/store/`) implement each port against a concrete
  backend. Swapping (e.g. SQLite → Postgres) means one new package in
  `server/store/`.

### Frontend

React 18 + TypeScript + Tailwind CSS + Vite + React Query. The API client is
auto-generated from `openapi.yaml` via `@hey-api/openapi-ts`. The build output
(`frontend/dist/`) is copied into `server/web/dist/` at Docker-build time and
baked into the Go binary by `//go:embed`, which the REST router serves with SPA
fallback at `/`.

## LLM Processing Loops

There are three distinct LLM call paths. Two are agentic (the model can invoke tools); one is a direct single-shot call.

---

### Loop 1 — OCR (`computer_vision` stage)

Single-shot vision call. No tool use. The model transcribes the image directly.

**Prompt:** [`prompts/ocr.txt`](prompts/ocr.txt) — instructs the model to produce structured Markdown. Optionally injects `document_context` if set.

```
PNG bytes
  │
  ▼
Ollama /api/generate  ←─ ocr.txt prompt (system) + image (user)
  │
  ▼  streamed tokens
parse raw text
  │
  ▼
SQLite: save run (ocr_raw field) → advance pipeline
```

---

### Loop 2 — LLM Text (`llm_text` stage: clarify, classify, …)

Agentic loop via [ADK-go](https://github.com/google/adk-go). The model calls `rag_search` as many times as it needs before producing a final answer.

**Prompts:**
- [`prompts/clarify.txt`](prompts/clarify.txt) — system instruction for the clarify stage. Injects `document_context`, `linked_context`, Q&A history from prior rounds, and previous output when re-running. Expects XML response: `<clarified_text>`, `<confidence>`, `<questions>`.
- [`prompts/classify.txt`](prompts/classify.txt) — system instruction for the classify stage. Injects linked context and document context. Expects JSON response: `{tags, summary, clarification_requests, confidence}`.

```
Render prompt template (system instruction)
  │   ├─ document_context (per-doc notes)
  │   ├─ linked_context (shared series/collection notes)
  │   ├─ Q&A history (answers from prior waiting rounds)
  │   └─ previous_output (when refining after review)
  │
  ▼
ADK Runner ── creates in-memory session
  │
  ├──► Ollama /api/chat  ◄─ system: rendered prompt  ◄─ user: OCR/input text
  │         │
  │    returns tool_calls=[{rag_search, {query, top_k}}]?
  │         │
  │    YES  ▼
  │    embed(query) → Qdrant search → [{text, title, score}]
  │    append tool response to session → call Ollama again
  │         │  (repeats until no tool calls)
  │    NO   ▼
  │    final text response
  │
  ▼
parse response (XML for clarify, JSON for classify)
  │   ├─ clarified_text / tags+summary
  │   ├─ confidence: high | medium | low
  │   └─ questions: [{segment, question}]
  │
  ├─ confidence < threshold OR questions present?
  │     YES → park job at `waiting` (human review)
  │     NO  → advance pipeline
  │
  ▼
SQLite: save run → SSE token event to browser
```

**Human review loop** (when job parks at `waiting`):

```
Human answers questions / edits text / approves
  │
  ▼
PUT /jobs/:id/status → pending
  │
  ▼
Worker re-runs stage with Q&A answers injected into prompt via QAHistory
```

---

### Loop 3 — Chat

Agentic loop, same `rag_search` tool as Loop 2. Conversation history is pre-loaded into the ADK session as real session events so the model sees genuine dialogue context.

**System instruction:** built inline in `server/api/rest/chat.go`. Optionally extended by the chat session's custom `system_prompt`.

```
Load message history from SQLite
  │
  ▼
ADK Runner
  ├─ inject prior turns as session events (user + assistant roles)
  ├─ system: "helpful assistant with rag_search tool" + optional custom prompt
  └─ user: current message
  │
  ├──► Ollama /api/chat
  │         │
  │    tool_calls=[{rag_search, {query, top_k}}]?
  │         │
  │    YES  ▼
  │    embed(query) → Qdrant → results appended to session → call again
  │         │  (repeats until no tool calls)
  │    NO   ▼
  │    final text response
  │
  ▼
collect sources from all rag_search responses
  │
  ▼
SSE stream to browser:
  event: sources  → [{title, text, score, date_month}]
  event: token    → full response text
  event: done
  │
  ▼
SQLite: save assistant message + sources
```

---

### The `rag_search` tool

Shared between loops 2 and 3. Defined in [`server/core/adk/tools/rag_search.go`](server/core/adk/tools/rag_search.go).

```
Input:  { query: string, top_k: int (1–10, default 5) }

embed(query) via Ollama embedding model
  │
  ▼
Qdrant vector search (cosine similarity)
  │
  ▼
Output: { results: [{text, title, date_month, score}] }
```

The tool is constructed once at `WorkerService` startup and shared across all `llm_text` stage runs. For chat, it is constructed per-request (handler has no shared state).

---

### Prompt → loop mapping

| Prompt file | Loop | Stage type | Response format |
|---|---|---|---|
| `prompts/ocr.txt` | OCR (direct) | `computer_vision` | plain Markdown |
| `prompts/clarify.txt` | LLM Text (agentic) | `llm_text` | XML tags |
| `prompts/classify.txt` | LLM Text (agentic) | `llm_text` | JSON object |
| _(inline in chat.go)_ | Chat (agentic) | REST handler | plain text |

---

## How to develop

### Add a pipeline stage

1. Add a stage entry to `config/pipeline.yaml`
2. Create a prompt file in `prompts/` if the stage is `llm_text` or `computer_vision`
3. If the stage needs a new type handler, add it to `server/core/worker.go`

### Change a prompt

Edit the relevant file in `prompts/`. The worker reloads prompts on each stage run.

### Add an embed destination

Add an entry under `destinations` in the `embed` stage in `config/pipeline.yaml`.
Implement the adapter in a new package under `server/store/` (see
`server/store/qdrant/` as a template) and register it in the embed stage handler
in `server/core/worker.go`.

### Regenerate the TypeScript API client

```bash
make generate-client
```

Runs `@hey-api/openapi-ts` against `openapi.yaml` into `frontend/src/generated/`.

### Query the vector store from Claude Code

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

## Sub-documentation

- [`config/README.md`](config/README.md) — pipeline.yaml reference

The SQL schema is defined by the `.up.sql` files in `db/migrations/`.
