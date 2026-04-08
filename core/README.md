# core/

Domain logic for the document pipeline. This package has no I/O imports — it never imports FastAPI, SQLite, httpx, or any adapter by name. All I/O is injected at the `app.py` level via function arguments.

## Hexagonal architecture

```
adapters/inbound/      →  core/services/  →  adapters/outbound/
  api.py                    ingest.py           sqlite.py
                            worker.py           filesystem.py
                                                ollama.py
                                                qdrant.py
                                                open_webui.py
```

Inbound adapters translate external requests into core service calls. Core services contain all business logic. Outbound adapters implement I/O.

---

## Domain model

### `core/domain/document.py` — `Document`

```python
@dataclass
class Document:
    id: str                  # UUID
    content_hash: str        # SHA-256 hex of the file bytes
    created_at: str          # ISO-8601 UTC
    updated_at: str          # ISO-8601 UTC
    title: str | None        # set post-OCR or from filename
    date_month: str | None   # 'YYYY-MM'
    png_path: str | None     # absolute path to source image
    duplicate_of: str | None # references another document id
    additional_context: str  # per-document LLM context
    linked_contexts: list    # list of context entry IDs
```

Documents are lightweight — they store source metadata. All processing state is in `Job`.

---

### `core/domain/job.py` — `Job`

```python
@dataclass
class Job:
    id: str
    document_id: str
    stage: str               # stage name from pipeline.yaml
    status: str              # pending|running|waiting|error|done
    options: dict            # {require_context, embed: {embed_image}}
    runs: list               # list of Run dicts (LLM round-trips)
    created_at: str
    updated_at: str
```

Jobs are the primary processing unit. One job per pipeline stage per document. `runs` is appended on each LLM execution and stores structured inputs, outputs, confidence, questions, and suggestions.

---

### `core/domain/pipeline.py` — `PipelineConfig` and `StageDefinition`

```python
@dataclass
class PipelineConfig:
    max_concurrent: int
    stages: list[StageDefinition]

@dataclass
class StageDefinition:
    name: str
    type: str               # 'computer_vision' | 'llm_text' | 'embed'
    model: str | None
    prompt: str | None      # path to prompt file, e.g. 'prompts/clarify.txt'
    input: str | None       # single input field name in stage data
    inputs: list | None     # multi-input field names
    output: str | None      # single output field name
    outputs: list | None    # multi-output specs [{field, type}]
    destinations: list | None  # embed stage only: [{type, url, collection, ...}]
    require_context: bool | None
    start_if: dict | None   # conditions to check before starting (else park as waiting)
    continue_if: list | None   # conditions to check after running (else park for review)
    skip_if: dict | None    # conditions to skip this stage entirely
    vision: bool | None     # pass original image to LLM alongside text
    save_as_artifact: bool | None  # persist LLM output as artifact
    max_concurrent: int | None     # override global concurrency
```

`PipelineConfig.from_yaml(path)` loads `config/pipeline.yaml` and performs `${VAR}` environment variable substitution at load time.

---

## Services

### `core/services/ingest.py`

Two ingest paths:

**`ingest(image_bytes, meta_json, attachment_filename, db, vault_path, filesystem)`**
- Called for webhook-received images (reMarkable)
- Hash → dedup check → save artifact → create Document → store ingest metadata in KV

**`ingest_upload(file_bytes, filename, file_type, title, additional_context, linked_contexts, db, vault_path, filesystem)`**
- Called by `POST /api/v1/documents`
- Same dedup and artifact logic; additionally handles `.txt`/`.md` (no image saved, raw text stored in KV meta)
- Auto-extracts title from filename stem or first non-blank line for text files
- Returns `None` on exact duplicate (SHA-256 collision)

Both functions return the created `Document`. The HTTP handler in `api.py` creates the first pipeline `Job` after ingest.

---

### `core/services/worker.py`

Background asyncio task started in `app.py` lifespan. Processes jobs stage by stage in pipeline order.

**Main loop:**

```
loop:
  for each stage in pipeline.yaml (in order):
    jobs = db.get_pending_jobs(stage.name)
    if not jobs: continue
    await process_batch(stage, jobs)
    await ollama.unload(stage.model)   # keep_alive=0 to free VRAM
    break  # restart from first stage (OCR has priority)
  else:
    await asyncio.sleep(5)
```

`max_concurrent` from `PipelineConfig` (or per-stage override) governs an `asyncio.Semaphore`.

**`process_batch(stage, jobs)`** — for each job:
1. Set status to `running`, append `stage_events` row
2. Dispatch to stage handler by `stage.type`
3. On success: write outputs to job `runs`, determine next stage, upsert next job at `pending`
4. On failure: count `failed` events for this stage; if `< 3` → reset to `pending` with backoff (2s/4s/8s); if `≥ 3` → set `error`

**Stage handlers:**

| Stage type | What it does |
|---|---|
| `computer_vision` | Call `ollama.generate_vision(model, prompt, image_bytes)` — skip if text file |
| `llm_text` | Check `start_if` (park if not met); render prompt with Jinja2 (context, Q&A history); call `ollama.generate_text`; parse outputs; check `continue_if` (park for review if not met) |
| `embed` | Call `ollama.generate_embed` on clarified text; upsert to each destination (Qdrant, Open WebUI) |

**Model unloading:** After each batch, `POST /api/generate` with `keep_alive=0` to free VRAM. Only called if the model was used.

**Startup recovery:** On startup, `db.reset_running()` resets any jobs stuck at `running` (from a crash) back to `pending`.

---

## Prompt templates

Plain text files in `prompts/` using [Jinja2](https://jinja.palletsprojects.com/) syntax. Available template variables depend on stage:

| Variable | Available in |
|---|---|
| `additional_context` | All LLM stages |
| `linked_context` | All LLM stages |
| `document_context` | All LLM stages |
| `ocr_raw` | clarify prompt |
| `clarified_text` | classify prompt |
| `qa_history` | clarify prompt (Q&A loop history) |

The worker renders prompts fresh on each stage run — no restart required to pick up prompt changes in development.
