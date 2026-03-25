# document-pipeline

A config-driven, multi-stage document ingestion pipeline for reMarkable tablet notes. Receives handwritten pages via webhook, runs them through OCR, LLM cleanup, and classification, then indexes them in a vector store for RAG queries from Open WebUI, Claude Code, and OpenCode.

## What it does

```
reMarkable tablet
  → rmfakecloud (self-hosted cloud replacement)
  → POST /webhook (this service)
  → OCR (computer vision model via Ollama)
  → Clarify (LLM cleanup + Q&A clarification loop)
      [parks for review if confidence < high]
  → Classify (tags + summary)
      [parks for review if confidence < high]
  → Embed → Qdrant + Open WebUI (vector stores)
```

All stages, models, prompts, and review gates are defined in [`config/pipeline.yaml`](config/README.md) — no code changes needed to add, remove, or reorder stages.

## Quickstart

### Prerequisites

- Docker + Docker Compose
- The `llm` stack from `home-server` running (Ollama + Open WebUI + Qdrant)
- A reMarkable tablet registered with rmfakecloud

### Environment variables

Copy from `home-server/.env`. Required variables per phase:

| Variable | Phase | Description |
|---|---|---|
| `OCR_MODEL` | 2 | Ollama model for OCR (e.g. `glm-ocr`) |
| `OLLAMA_BASE_URL` | 2 | Ollama endpoint (e.g. `http://ollama:11434`) |
| `CLARIFY_MODEL` | 3 | Ollama model for OCR cleanup (e.g. `qwen3-4b-32k`) |
| `CLASSIFY_MODEL` | 4 | Ollama model for classification (e.g. `qwen3-4b-32k`) |
| `EMBED_MODEL` | 5 | Ollama embedding model (e.g. `nomic-embed-text:v1.5`) |
| `QDRANT_URL` | 5 | Qdrant endpoint (e.g. `http://qdrant:6333`) |
| `QDRANT_COLLECTION` | 5 | Collection name (e.g. `remarkable`) |
| `QDRANT_API_KEY` | 5 | Optional Qdrant API key |
| `OPEN_WEBUI_URL` | 5 | Open WebUI base URL |
| `OPEN_WEBUI_API_KEY` | 5 | Open WebUI API key |
| `OPEN_WEBUI_KNOWLEDGE_ID` | 5 | Knowledge base ID in Open WebUI |

### Run (standalone dev)

```bash
docker compose up --build
```

The service listens on port 8000. The UI is at `http://localhost:8000`.

In production the service is deployed via `home-server/notes/docker-compose.yml` under the container name `remarkable-bridge`.

## Project structure

```
document-pipeline/
├── core/           Domain logic — state machine, pipeline config, services
├── adapters/       I/O adapters — inbound (webhook, UI) and outbound (Ollama, SQLite, Qdrant)
├── config/         pipeline.yaml + documentation
├── store/          Database schema documentation
├── ui/             Jinja2 templates (HTMX-powered, no build step)
├── api/            API reference documentation
└── prompts/        LLM prompt templates (plain text, edit without touching code)
```

See [`core/README.md`](core/README.md) for architecture details.

## Hexagonal architecture

This project follows a structural hexagonal pattern:

- **Core** (`core/`) contains all business logic and has no I/O imports. It calls functions in the outbound adapters but never imports FastAPI, SQLite, or httpx by name.
- **Inbound adapters** (`adapters/inbound/`) translate external requests (HTTP, webhook) into core service calls.
- **Outbound adapters** (`adapters/outbound/`) implement the actual I/O: database, filesystem, Ollama, Qdrant.

Swapping an adapter (e.g. replacing SQLite with Postgres, or Qdrant with another vector DB) means editing one file in `adapters/outbound/` and updating `app.py`.

## How to develop

### Add a new pipeline stage

1. Add a stage entry to `config/pipeline.yaml`
2. Create a prompt file in `prompts/` if the stage is `llm_text` or `computer_vision`
3. If the stage needs a new type handler, add it to `adapters/outbound/ollama.py` and register it in `core/domain/pipeline.py`

### Change a prompt

Edit the relevant file in `prompts/`. The worker reloads prompts on each stage run (no restart needed in dev, restart required in prod).

### Add an embed destination

Add an entry under `destinations` in the `embed` stage in `config/pipeline.yaml`. Implement the adapter in `adapters/outbound/` and register it in the embed stage handler.

## Sub-documentation

- [`PLAN.md`](PLAN.md) — delivery phases and current status
- [`core/README.md`](core/README.md) — architecture, domain model, services
- [`store/README.md`](store/README.md) — database schema
- [`config/README.md`](config/README.md) — pipeline.yaml reference
- [`ui/README.md`](ui/README.md) — UI guide
- [`api/README.md`](api/README.md) — API reference
