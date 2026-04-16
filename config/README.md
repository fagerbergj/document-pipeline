# config/

Pipeline configuration. Everything about what stages run, in what order, with which models and prompts, is defined here. Adding or removing a stage requires only editing `pipeline.yaml` — no code changes.

---

## `pipeline.yaml` reference

### Top-level keys

| Key | Type | Required | Description |
|---|---|---|---|
| `max_concurrent` | int | yes | Max LLM calls in flight at once. Use `1` on a single-GPU machine. |
| `stages` | list | yes | Ordered list of stage definitions. Stages run in list order. |

### Stage definition

| Key | Type | Required | Stage types | Description |
|---|---|---|---|---|
| `name` | string | yes | all | Unique stage identifier. Used as key in `stage_data` and in `stage_events`. |
| `type` | string | yes | all | `computer_vision` \| `llm_text` \| `embed` |
| `model` | string | `computer_vision`, `llm_text`, `embed` | — | Ollama model name. Supports `${VAR}` substitution. |
| `prompt` | string | `computer_vision`, `llm_text` | — | Path to prompt file (relative to repo root). |
| `input` | string | `llm_text`, `embed` | — | Field name in `stage_data` to use as input text. |
| `output` | string | `llm_text` | — | Field name in `stage_data` to write result to. Use when there is a single text output. |
| `outputs` | list | `computer_vision`, `llm_text` | — | Multi-output spec. Use instead of `output` when there are multiple outputs or non-text types. See below. |
| `start_if` | dict | `llm_text` | — | Conditions required to start the stage. If not met, parks as `waiting`. Keys: `context_provided`. |
| `continue_if` | list | `llm_text` | — | List of rules; if none match, parks as `waiting` for human review. Each rule is a dict of AND'd conditions (e.g. `{confidence: high}` or `{user_approves: true}`). Rules in the list are OR'd. |
| `metadata_fields` | list | `embed` | — | Fields from `documents` and `stage_data` to include as Qdrant payload. |
| `destinations` | list | `embed` | — | List of embed sink definitions. See below. |

### `outputs` item

| Key | Type | Values | Description |
|---|---|---|---|
| `field` | string | — | Key to write in `stage_data.<stage_name>` |
| `type` | string | `text` \| `json_array` \| `diagrams` | `diagrams` triggers sidecar PNG+JSON extraction from the model response |

### `destinations` item (embed stage)

| Key | Type | Required | Description |
|---|---|---|---|
| `type` | string | yes | `qdrant` or `open_webui` |
| `url` | string | yes | Base URL of the destination. Supports `${VAR}`. |
| `api_key` | string | no | API key. Supports `${VAR}`. |
| `collection` | string | `qdrant` only | Qdrant collection name. Supports `${VAR}`. |
| `knowledge_id` | string | `open_webui` only | Open WebUI knowledge base ID. Supports `${VAR}`. |

---

## Environment variable substitution

Values in `pipeline.yaml` can reference environment variables using `${VAR}` syntax. Substitution happens once at startup when `PipelineConfig` is loaded. If a referenced variable is not set, the value is left as the literal string `${VAR}` (no error at load time — the stage will fail when it tries to use the empty model name).

---

## Environment variables

| Variable | Phase | Stage | Description |
|---|---|---|---|
| `OCR_MODEL` | 2 | `ocr` | Ollama model for OCR (e.g. `qwen3-vl:30b`) |
| `OLLAMA_URL` | 2 | all LLM stages | Ollama endpoint (e.g. `http://ollama:11434`) |
| `CLARIFY_MODEL` | 3 | `clarify` | Ollama model for OCR cleanup (e.g. `gemma4:31b`) |
| `CLASSIFY_MODEL` | 4 | `classify` | Ollama model for classification (e.g. `gemma4-26b:latest`) |
| `EMBED_MODEL` | 5 | `embed` | Ollama embedding model (e.g. `nomic-embed-text:v1.5`) |
| `QDRANT_URL` | 5 | `embed` | Qdrant base URL (e.g. `http://qdrant:6333`) |
| `QDRANT_COLLECTION` | 5 | `embed` | Qdrant collection name (e.g. `remarkable`) |
| `QDRANT_API_KEY` | 5 | `embed` | Optional Qdrant API key |
| `OPEN_WEBUI_URL` | 5 | `embed` | Open WebUI base URL |
| `OPEN_WEBUI_API_KEY` | 5 | `embed` | Open WebUI API key |
| `OPEN_WEBUI_KNOWLEDGE_ID` | 5 | `embed` | Knowledge base ID in Open WebUI |

All variables are also listed in the root `README.md`.

---

## Full `pipeline.yaml` example

```yaml
max_concurrent: 1

stages:
  - name: ocr
    type: computer_vision
    model: ${OCR_MODEL}
    prompt: prompts/ocr.txt
    outputs:
      - field: ocr_raw
        type: text

  - name: clarify
    type: llm_text
    model: ${CLARIFY_MODEL}
    prompt: prompts/clarify.txt
    input: ocr_raw
    output: clarified_text
    start_if:
      context_provided: true
    continue_if:
      - confidence: high

  - name: classify
    type: llm_text
    model: ${CLASSIFY_MODEL}
    prompt: prompts/classify.txt
    input: clarified_text
    outputs:
      - field: tags
        type: json_array
      - field: summary
        type: text
    start_if:
      context_provided: true
    continue_if:
      - confidence: high

  - name: embed
    type: embed
    model: ${EMBED_MODEL}
    input: clarified_text
    metadata_fields: [title, tags, summary, date_month]
    destinations:
      - type: qdrant
        url: ${QDRANT_URL}
        collection: ${QDRANT_COLLECTION}
        api_key: ${QDRANT_API_KEY}
      - type: open_webui
        url: ${OPEN_WEBUI_URL}
        api_key: ${OPEN_WEBUI_API_KEY}
        knowledge_id: ${OPEN_WEBUI_KNOWLEDGE_ID}
```

---

## How-to examples

### Add a new LLM stage

```yaml
  - name: my_stage
    type: llm_text
    model: ${MY_MODEL}
    prompt: prompts/my_stage.txt
    input: clarified_text
    output: my_output
```

Create `prompts/my_stage.txt` with the prompt. Set `MY_MODEL` in `.env`. No code changes required.

### Always require human review for a stage

Add `continue_if: [{user_approves: true}]` to an `llm_text` stage. This rule can never be auto-satisfied, so every document will park for human review after that stage runs.

### Disable a stage temporarily

Comment out the stage in `pipeline.yaml` and restart. Documents already at that stage will stay in `pending` state until the stage is re-added.

### Change the embed model

Update `EMBED_MODEL` in `.env` and restart. Existing vectors in Qdrant were generated with the old model — they will not be automatically regenerated. Reprocess affected documents from the `embed` stage if needed.
