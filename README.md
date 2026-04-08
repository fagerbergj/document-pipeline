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
| `OLLAMA_BASE_URL` | 2 | Ollama endpoint (e.g. `http://ollama:11434`) |
| `OCR_MODEL` | 2 | Ollama model for OCR (e.g. `glm-ocr`) |
| `CLARIFY_MODEL` | 3 | Ollama model for clarification (e.g. `qwen3:4b`) |
| `CLASSIFY_MODEL` | 4 | Ollama model for classification (e.g. `qwen3:4b`) |
| `EMBED_MODEL` | 5 | Ollama embedding model (e.g. `nomic-embed-text:v1.5`) |
| `QDRANT_URL` | 5 | Qdrant endpoint (e.g. `http://qdrant:6333`) |
| `QDRANT_COLLECTION` | 5 | Collection name (e.g. `remarkable`) |
| `QDRANT_API_KEY` | 5 | Optional Qdrant API key |
| `OPEN_WEBUI_URL` | 5 | Open WebUI base URL |
| `OPEN_WEBUI_API_KEY` | 5 | Open WebUI API key |
| `OPEN_WEBUI_KNOWLEDGE_ID` | 5 | Knowledge base ID in Open WebUI |
| `QUERY_MODEL` | chat | Model for RAG chat (defaults to `CLARIFY_MODEL`) |

Optional:

| Variable | Default | Description |
|---|---|---|
| `DB_PATH` | `/data/pipeline.db` | SQLite database path |
| `VAULT_PATH` | `/vault` | Artifact storage directory |

### Run

```bash
docker compose up --build
```

The service listens on port 8000. The UI is at `http://localhost:8000`.

In production the service is deployed via `home-server/notes/docker-compose.yml` under the container name `remarkable-bridge`.

## Project structure

```
document-pipeline/
├── core/           Domain logic — models, pipeline config, services (ingest, worker)
├── adapters/       I/O adapters — inbound (REST API) and outbound (SQLite, Ollama, Qdrant, Open WebUI)
├── config/         pipeline.yaml + documentation
├── db/             Database schema documentation
├── api/            API reference documentation
├── frontend/       React + Vite UI (TypeScript + Tailwind + React Query, built into frontend/dist/)
└── prompts/        LLM prompt templates (plain text — edit without touching code)
```

## Architecture

### Hexagonal

```
adapters/inbound/      →  core/services/  →  adapters/outbound/
  api.py                    ingest.py           sqlite.py
                            worker.py           filesystem.py
                                                ollama.py
                                                qdrant.py
                                                open_webui.py
                                                streams.py
```

- **Core** (`core/`) contains all business logic with no I/O imports
- **Inbound adapters** (`adapters/inbound/`) translate HTTP requests into core service calls
- **Outbound adapters** (`adapters/outbound/`) implement the actual I/O: database, filesystem, Ollama, Qdrant, Open WebUI

Swapping an adapter (e.g. replacing SQLite with Postgres) means editing one file in `adapters/outbound/`.

### Frontend

React 18 + TypeScript + Tailwind CSS + Vite + React Query. The API client is auto-generated from the OpenAPI schema via `@hey-api/openapi-ts`. Built into `frontend/dist/` and served as static assets by the FastAPI app.

## How to develop

### Add a pipeline stage

1. Add a stage entry to `config/pipeline.yaml`
2. Create a prompt file in `prompts/` if the stage is `llm_text` or `computer_vision`
3. If the stage needs a new type handler, add it to `core/services/worker.py`

### Change a prompt

Edit the relevant file in `prompts/`. The worker reloads prompts on each stage run.

### Add an embed destination

Add an entry under `destinations` in the `embed` stage in `config/pipeline.yaml`. Implement the adapter in `adapters/outbound/` and register it in the embed stage handler in `worker.py`.

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

- [`core/README.md`](core/README.md) — architecture, domain model, services
- [`db/README.md`](db/README.md) — database schema and query patterns
- [`config/README.md`](config/README.md) — pipeline.yaml reference
- [`api/README.md`](api/README.md) — API reference
- [`frontend/README.md`](frontend/README.md) — frontend architecture and dev guide
