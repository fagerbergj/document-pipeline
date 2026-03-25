# Delivery Plan

Each phase (except Phase 1) ships on its own branch and merges to `main` only after successful manual testing with a real reMarkable document.

| Phase | Branch | Scope | Status |
|---|---|---|---|
| 1 | `main` | Docs, structure, config, prompts — no functional code | ✅ Done |
| 2 | `phase/2-ocr` | Replace bridge: webhook + OCR + dashboard UI | ✅ Done |
| 3 | `phase/3-clarify` | Clarify stage + review UI + clarification Q&A loop | 🔲 Pending |
| 4 | `phase/4-classify` | Classify stage + duplicate detection + delete | 🔲 Pending |
| 5 | `phase/5-embed` | Qdrant + embed stage + Open WebUI via VECTOR_DB=qdrant | 🔲 Pending |

---

## Phase 1 — Docs & Structure

**Branch:** `main`

Establishes the repo structure, all documentation, config, and prompt drafts. No functional code. This commit is the design anchor — if future phases drift, the READMEs and plan file redirect back to the original intent.

**Deliverables:**
- Full directory structure
- All README files (see links below)
- `config/pipeline.yaml` — complete pipeline definition
- `prompts/ocr.txt`, `prompts/clarify.txt`, `prompts/classify.txt`
- `.gitignore`, `requirements.txt`, `Dockerfile`

**Reference docs:** [README](README.md) · [core](core/README.md) · [store](store/README.md) · [config](config/README.md) · [ui](ui/README.md) · [api](api/README.md)

---

## Phase 2 — Replace Bridge (OCR only)

**Branch:** `phase/2-ocr`

Replaces `home-server/notes/bridge/` with the new service. The webhook URL (`POST /webhook` on container `remarkable-bridge:8000`) must not change — rmfakecloud is hardcoded to it.

**What ships:**
- SQLite DB with all three tables (`documents`, `stage_events`, `document_destinations`)
- Webhook handler: hash PNG, dedup, save to disk, insert DB row, return immediately
- Async worker: batch OCR with `computer_vision` stage, unload model after batch
- Dashboard UI: status counts + document table, click row to see OCR text
- `home-server/notes/docker-compose.yml` updated: new image, vault path `/mnt/personal01/remarkable`

**Does not yet implement:** clarify, classify, embed, review UI, duplicate resolution

**Test:** Send a note from the reMarkable tablet. Verify PNG appears in `/mnt/personal01/remarkable/YYYY-MM/`, DB row is created, dashboard at `:3006` shows the document with OCR text.

**Reference docs:** [core](core/README.md) · [store](store/README.md) · [api](api/README.md)

---

## Phase 3 — Clarify Stage + Review UI

**Branch:** `phase/3-clarify`

Adds the clarify LLM stage and the review UI. The clarify stage can emit `clarification_requests` — segments it couldn't resolve — which surface in the review UI for the user to answer before re-running.

**What ships:**
- Clarify stage batch processing in worker
- Review service: approve, reject, re-run with clarification responses
- Review inbox UI (`/review`) with diff view and clarification panel
- Retry logic: 3 retries with 2s/4s/8s backoff, then error state
- Reprocess-from-stage action

**Test:** Confirm clarify runs after OCR. Verify `review_clarify:waiting` appears in review tab. Respond to a clarification request, re-run, approve, confirm `classify:pending`.

**Reference docs:** [core](core/README.md) · [config](config/README.md) · [ui](ui/README.md)

---

## Phase 4 — Classify Stage + Duplicate/Delete

**Branch:** `phase/4-classify`

Adds the classify stage and handles document lifecycle: duplicate detection, replace-existing, and delete.

**What ships:**
- Classify stage in worker
- Post-OCR duplicate title detection → `duplicate_review` queue
- Duplicate resolution UI: keep both / replace existing / discard
- Delete action: removes from Qdrant + soft-deletes DB row
- Error tab with retry / reprocess actions

**Test:** Confirm classify runs, `review_classify:waiting` appears. Approve tags. Send the same note twice to trigger duplicate review. Test delete.

**Reference docs:** [store](store/README.md) · [ui](ui/README.md)

---

## Phase 5 — Qdrant + Embed

**Branch:** `phase/5-embed`

Adds the embed stage and wires up Qdrant. Open WebUI is configured to read from the same Qdrant collection directly (`VECTOR_DB=qdrant`) — no separate push needed.

**What ships:**
- Embed stage in worker (Ollama embeddings → Qdrant upsert)
- `adapters/outbound/qdrant.py`
- Qdrant added to `home-server/llm/docker-compose.yml`
- Open WebUI env: `VECTOR_DB=qdrant`, `QDRANT_URI=http://qdrant:6333`
- MCP server config documented for Claude Code
- OpenCode query config documented

**Test:** Full pipeline run. Verify document appears in Qdrant collection (`GET http://localhost:6333/collections/remarkable`). Query from Open WebUI. Query via Claude Code MCP tool.

**Reference docs:** [config](config/README.md) · [api](api/README.md)
