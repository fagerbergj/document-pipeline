# Document Pipeline

A full-stack application that processes handwritten notes from a reMarkable tablet through an OCR → augmentation → classification → embedding pipeline, storing results in a vector database for natural language querying.

---

## System Architecture

```
reMarkable Tablet
      │
      ▼ (PDF via rmfakecloud webhook)
┌─────────────────┐
│   Ingest        │  Save PDF + extract page images
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│   OCR Stage     │  glm-ocr (Ollama vision model)
│                 │  Outputs: ocr_raw (markdown)
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│   Clarify Stage │  LLM text (gpt-oss-20b-64k)
│                 │  Augments/corrects using provided context
│                 │  Skipped if no context provided
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  Classify Stage │  LLM text (qwen3-4b-32k)
│                 │  Outputs: tags (JSON array), summary
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│   Embed Stage   │  nomic-embed-text
│                 │  → Qdrant, Open WebUI knowledge base
└─────────────────┘
```

---

## Technology Stack

| Component | Technology |
|-----------|------------|
| OCR | glm-ocr via Ollama |
| LLM | Ollama (gpt-oss-20b-64k, qwen3-4b-32k) |
| Embeddings | nomic-embed-text via Ollama |
| Vector DB | Qdrant |
| Backend | FastAPI + Python (async) |
| Frontend | React + TypeScript |
| Database | SQLite (aiosqlite) |
| Storage | Local disk (vault) |
| PDF → image | PyMuPDF (fitz) |
| Orchestration | Docker Compose |

---

## Project Structure

```
document-pipeline/
├── app.py                        # FastAPI entrypoint
├── config/
│   └── pipeline.yaml             # Stage definitions (model, prompt, outputs)
├── prompts/                      # Per-stage prompt files
├── core/
│   ├── domain/
│   │   └── document.py           # Document dataclass
│   ├── services/
│   │   ├── ingest.py             # Webhook ingestion, PDF → page images
│   │   └── worker.py             # Stage execution loop
│   └── config.py                 # PipelineConfig loader
├── adapters/
│   ├── inbound/
│   │   └── webhook.py            # reMarkable webhook receiver
│   └── outbound/
│       ├── ollama.py             # Vision + text + embed Ollama calls
│       ├── filesystem.py         # PNG/PDF storage, pdf_to_page_images
│       ├── sqlite.py             # Document persistence
│       ├── qdrant.py             # Vector DB adapter
│       ├── open_webui.py         # Open WebUI knowledge sync
│       └── streams.py            # SSE streaming
└── frontend/                     # React + TypeScript UI
```

---

## Pipeline Config

Defined in `config/pipeline.yaml`. Each stage has:
- `type`: `computer_vision` | `llm_text` | `embed`
- `model`: resolved from env var (e.g. `${OCR_MODEL}`)
- `prompt`: path to prompt file
- `start_if` / `continue_if`: conditional execution guards
- `outputs`: field names and types written to `stage_data`

---

## Multi-page PDF Support

At ingest, if the attachment is a PDF (`%PDF` magic bytes):
1. Raw PDF saved as `<hash>.pdf`
2. Each page rendered to PNG via PyMuPDF at 150 DPI → `<hash>_p1.png`, `<hash>_p2.png`, ...
3. `png_path` = first page (used for display)
4. `page_images` = all page paths (used by OCR stage)

Single images (PNG) are stored directly; `page_images = [png_path]`.

---

## Environment Variables

| Variable | Description |
|----------|-------------|
| `OCR_MODEL` | Vision model for OCR stage (e.g. `glm-ocr:latest`) |
| `CLARIFY_MODEL` | LLM for clarify stage (e.g. `gpt-oss-20b-64k`) |
| `CLASSIFY_MODEL` | LLM for classify stage (e.g. `qwen3-4b-32k`) |
| `EMBED_MODEL` | Embedding model (e.g. `nomic-embed-text`) |
| `OLLAMA_BASE_URL` | Ollama API base URL |
| `VAULT_PATH` | Root path for storing images/PDFs |
| `DB_PATH` | SQLite database file path |
| `QDRANT_URL` | Qdrant server URL |
| `QDRANT_COLLECTION` | Qdrant collection name |
| `OPEN_WEBUI_URL` | Open WebUI base URL |
| `OPEN_WEBUI_API_KEY` | Open WebUI API key |
| `OPEN_WEBUI_KNOWLEDGE_ID` | Knowledge base ID to sync into |

---

## Deployment

Runs as `document-pipeline` service in `~/home-server/notes/docker-compose.yml`.
- Port: `3006 → 8000`
- Vault mounted at `/mnt/personal01/remarkable`
- On the `llm_default` network (shared with Ollama)
- Proxied via the home server reverse proxy at port 3006
