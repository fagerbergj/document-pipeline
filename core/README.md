# core/

Domain logic for the document pipeline. No external I/O — this package never imports FastAPI, SQLite, httpx, or any adapter by name. It calls functions that adapters provide, but those are injected at the `app.py` level.

## Hexagonal architecture

```
adapters/inbound/      →  core/services/  →  adapters/outbound/
  webhook.py                ingest.py           sqlite.py
  api.py                    worker.py           filesystem.py
                            review.py           ollama.py
                                                qdrant.py
                                                open_webui.py
```

Inbound adapters translate external requests into core service calls. Core services contain all business logic. Outbound adapters implement I/O. Core never knows about HTTP status codes, SQL syntax, or file paths — it reasons in terms of `Document` entities and service results.

Swapping an adapter (e.g. replacing SQLite with Postgres) means editing one file in `adapters/outbound/` and updating `app.py`.

---

## Domain model

### `core/domain/document.py`

#### `Document` entity

```python
@dataclass
class Document:
    id: str                  # UUID
    content_hash: str        # SHA-256 of the PNG bytes
    created_at: str          # ISO-8601 UTC
    updated_at: str          # ISO-8601 UTC
    current_stage: str       # stage name from pipeline.yaml, or 'done'/'deleted'
    stage_state: str         # 'pending'|'running'|'waiting'|'error'|'done'
    title: str | None
    date_month: str | None   # 'YYYY-MM'
    png_path: str | None
    duplicate_of: str | None # references documents.id
    stage_data: dict         # keyed by stage name, free-form JSON
```

#### State machine helpers

`advance(doc, next_stage)` — returns a new `Document` with `current_stage=next_stage, stage_state='pending'`.

`set_waiting(doc)` — sets `stage_state='waiting'` (for stages parked awaiting context or review).

`set_running(doc)` — sets `stage_state='running'`.

`set_error(doc)` — sets `stage_state='error'`.

`set_done(doc)` — sets `current_stage='done', stage_state='done'`.

`set_deleted(doc)` — sets `current_stage='deleted', stage_state='done'`.

### `core/domain/pipeline.py`

#### `PipelineConfig`

Loaded once at startup from `config/pipeline.yaml`. Performs `${VAR}` env var substitution at load time.

```python
@dataclass
class PipelineConfig:
    max_concurrent: int
    stages: list[StageDefinition]
```

#### `StageDefinition`

```python
@dataclass
class StageDefinition:
    name: str
    type: str          # 'computer_vision'|'llm_text'|'embed'
    model: str | None
    prompt: str | None  # path to prompt file, e.g. 'prompts/ocr.txt'
    input: str | None   # field name in stage_data to pass as input
    output: str | None  # field name to write result to in stage_data
    outputs: list[dict] | None   # multi-output spec [{field, type}]
    destinations: list[dict] | None  # embed stage only
    start_if: dict | None        # conditions to start stage (else park as waiting)
    continue_if: list[dict] | None   # conditions to auto-advance (else park for review)
```

---

## Services

### `core/services/ingest.py`

Called by `adapters/inbound/webhook.py` on each `POST /webhook`.

**`ingest(image_bytes, meta_json, attachment_filename) → Document | None`**

1. Compute `content_hash = sha256(image_bytes).hexdigest()`
2. Check for existing document with same hash → if found, return `None` (caller returns 200)
3. Determine `date_month` from current UTC datetime
4. Write PNG to filesystem: `<vault>/<YYYY-MM>/<hash[:8]>.png`
5. Insert `Document` row with `current_stage='ocr', stage_state='pending'`
6. Append `stage_events` row: `event_type='started'` (for the webhook receipt, not the OCR stage)
7. Return the new `Document`

Returns `None` on exact duplicate (same PNG bytes), returns the `Document` on success. All I/O calls go through outbound adapters passed as function arguments.

---

### `core/services/worker.py`

Runs as a background asyncio task started in `app.py`. Processes documents stage by stage in pipeline order.

**Batching loop:**

```
loop:
  processed = False
  for each stage in pipeline.yaml (in order):
    docs = db.get_pending(stage.name)
    if not docs:
      continue
    await process_batch(stage, docs)
    await ollama.unload(stage.model)   # keep_alive=0
    processed = True
  if not processed:
    await asyncio.sleep(5)
```

`max_concurrent` from `PipelineConfig` governs an `asyncio.Semaphore` applied within `process_batch`.

**`process_batch(stage, docs)`**

For each doc:
1. Mark `stage_state='running'`, append `stage_events` row `event_type='started'`
2. Dispatch to stage handler by `stage.type`
3. On success: write outputs to `doc.stage_data[stage.name]`, determine next stage, call `advance()`
4. On failure: increment retry check via `stage_events` COUNT for this stage; if `< 3` reset to `pending`; else set `error`
5. Backoff: 2s / 4s / 8s (exponential, based on retry count)

**Stage handlers:**

| Stage type | Handler |
|---|---|
| `computer_vision` | Call `ollama.generate_vision(model, prompt, image_bytes)` |
| `llm_text` | Check `start_if` (park as waiting if not met); call `ollama.generate_text`; check `continue_if` (park for review if not met) |
| `embed` | Call `ollama.embed(model, text)` → upsert to each destination (qdrant, open_webui) |

**Post-OCR duplicate title check:**
After OCR completes, before advancing to the next stage, check if another document with the same `title` and `stage_state != 'deleted'` exists. If so, set `current_stage='duplicate_review', stage_state='waiting'`.

**Model unloading:**
After each batch, `POST /api/generate` with `keep_alive=0` to Ollama to free VRAM. Only called if the model was actually used (batch was non-empty).

---

### `core/services/review.py`

Called by `adapters/inbound/api.py` for review actions.

**`approve(doc, config, db, now_str) → Document`**
- Advance document to the next stage in pipeline order
- Append `stage_events` row `event_type='approved'`
- Return updated document

**`reject(doc, config, db, now_str) → Document`**
- Reset `stage_state='pending'` on the current stage so the worker re-runs it
- Append `stage_events` row `event_type='rejected'`
- Return updated document

**`reject_with_clarifications(doc, clarification_responses, config, db, now_str, free_prompt="") → Document`**
- Appends a Q&A round to `stage_data.<stage>.qa_history`
- Clears `clarification_requests` (will be regenerated on re-run)
- Resets `stage_state='pending'` (worker re-runs with Q&A history as additional context)
- Append `stage_events` row `event_type='clarified'`
- Return updated document
