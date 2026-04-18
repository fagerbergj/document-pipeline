# document-pipeline

A config-driven, multi-stage document ingestion pipeline. Upload handwritten reMarkable notes or text/image files, run them through OCR, LLM cleanup, and classification, then index them in a vector store for RAG queries from Open WebUI, Claude Code, and OpenCode.

## What it does

```
Upload (PNG / JPG / TXT / MD)
  → OCR (computer vision model via Ollama)
  → Clarify (LLM cleanup + Q&A clarification loop)
      [parks for human review if confidence < high]
  → Classify (tags + summary)
      [parks for human review if confidence < high]
  → Embed → Qdrant + Open WebUI (vector stores)
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

### Chat / RAG

A built-in chat interface sends queries to an embedding model, retrieves matching notes from Qdrant, and streams LLM responses with cited sources. Chat sessions persist with full message history.

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
│   │                  filesystem, stream, prompts, config, embed (coordinator)
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
