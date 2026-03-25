# ui/

HTMX + Jinja2 templates. No build step. FastAPI serves the templates directly; HTMX handles partial page updates without a full reload.

## Access

The UI runs on the same port as the API (default `8000`). In production the container is mapped to host port `3006`.

- Dashboard: `http://localhost:3006/`
- Review inbox: `http://localhost:3006/review`
- Duplicate queue: `http://localhost:3006/duplicate`
- Error list: `http://localhost:3006/errors`

---

## Dashboard tab (`/`)

Shows all documents currently in the pipeline. Refreshes the table every 10 seconds via HTMX polling.

**Columns:** Title · Stage · State · Date received · Actions

**Status counts** at the top show totals by state: pending, running, waiting, error, done.

**Row click:** Expands inline to show the raw `stage_data` JSON for that document — useful for inspecting OCR text or LLM outputs without leaving the page.

**Actions per row:**
- Reprocess from stage (dropdown of past stages)
- Delete (soft-delete, removes from Qdrant, requires confirmation)

---

## Review tab (`/review`)

Shows all documents with `stage_state='waiting'` at a `manual_review` stage. Each item shows:

- **Stage data diff**: what changed between the previous checkpoint and this one. Fields that were added or modified are highlighted.
- **Editable fields**: all `stage_data` fields for the current stage are shown as editable text areas or tag inputs. Edits are saved before the approve/reject action fires.
- **Clarification panel** (only shown when the preceding stage emitted `clarification_requests`): one response field per unresolved clarification request. E.g.:
  > Segment: `"qu??k"` — Did you write "quick" or "quiet"?
  > [text input for your answer]

**Actions:**
- **Approve** — saves any edits, advances to the next stage
- **Re-run with clarifications** — saves clarification responses, resets the preceding LLM stage to `pending` so the worker re-runs it with the Q&A pairs as additional context
- **Reject** — saves any edits, resets the preceding non-review stage to `pending`

---

## Duplicate review tab (`/duplicate`)

Shows documents in `current_stage='duplicate_review', stage_state='waiting'`. These are new documents where post-OCR title matching found an existing document with the same title.

The tab shows both documents side by side:
- **Incoming**: the new document (stage_data.ocr.ocr_raw, title, date)
- **Existing**: the previously processed document (same fields + final processed text if available)

**Resolution options:**
- **Keep both** — advance the incoming document to the next stage; existing document is untouched
- **Replace existing** — soft-delete the existing document and remove it from Qdrant; advance the incoming document
- **Discard** — soft-delete the incoming document; existing document is untouched

---

## Errors tab (`/errors`)

Shows all documents with `stage_state='error'` (failed 3 times with exponential backoff).

**Columns:** Title · Stage · Last error · Last attempt

**Actions per row:**
- **Retry** — resets `stage_state='pending'` for the current stage (retry counter does not reset; the worker will retry up to 3 more times)
- **Reprocess from** — dropdown to pick an earlier stage; resets the document to that stage as `pending`
- **Delete** — soft-delete

---

## Templates

```
ui/templates/
├── base.html                    # Nav tabs, HTMX script, shared layout
├── dashboard.html               # Document table + status counts
├── review.html                  # Review inbox with diff view + clarification panel
├── duplicate.html               # Side-by-side duplicate resolution
├── errors.html                  # Error list with retry actions
└── partials/
    ├── document_row.html        # Single row in dashboard table (used for HTMX swap)
    ├── review_item.html         # Single review item (used for HTMX swap)
    └── status_counts.html       # Status count badges (used for HTMX polling)
```

All partial templates are designed for HTMX out-of-band swaps — the full-page templates include them by reference, and HTMX actions target them directly for updates without reloading the full page.
