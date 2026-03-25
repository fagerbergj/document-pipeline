# ui/

HTMX + Jinja2 templates. No build step. FastAPI serves the templates directly; HTMX handles partial page updates without a full reload.

## Access

The UI runs on the same port as the API (default `8000`). In production the container is mapped to host port `3006`.

- Dashboard: `http://localhost:3006/`
- Document detail / review: `http://localhost:3006/documents/{id}`
- Context library: `http://localhost:3006/contexts`

---

## Dashboard (`/`)

Shows all documents currently in the pipeline. Supports filter by stage/state and sort by date or title. Refreshes the table and status counts via HTMX polling.

**Columns:** Title · Stage · State · Date received · Actions

**Status counts** at the top show totals by state: pending, running, waiting, error, done.

**Row click:** Expands inline to show the OCR text and completed stage outputs for that document.

**Actions per row:**
- Replay from stage (dropdown of completed stages)
- Retry (errored documents)
- Stop (running documents)
- Set title

---

## Document detail page (`/documents/{id}`)

The document detail page is also where all review actions take place. There is no separate `/review` route.

**When a stage is parked waiting for context** (`context_required`): shows a form to set document context, which resets the stage to `pending` when submitted.

**When a stage is parked for human review** (ran, has LLM output, `stage_state='waiting'`):
- Single-output stages (e.g. `clarify`): side-by-side diff of input vs. LLM output, with an editable text area for the output
- Multi-output stages (e.g. `classify`): approve/reject buttons; outputs shown in the stage results section
- All parked stages: re-run with instructions form (free-text prompt), clarification Q&A panel if the LLM emitted `clarification_requests`

**Review actions:**
- **Approve** — saves any edits, advances to the next stage
- **Reject** — resets the current stage to `pending` for re-run
- **Re-run with instructions** — stores clarification responses and/or a free-text prompt into `qa_history`, resets the stage to `pending`

**Other actions available on this page:** set title, set/update document context, replay from a prior stage, retry (error state), stop (running state).

---

## Context library (`/contexts`)

Manage reusable document context snippets. Contexts can be applied to documents that are parked waiting for context before an LLM stage.

---

## Templates

```
ui/templates/
├── base.html                         # Nav, HTMX script, shared layout
├── dashboard.html                    # Document table + status counts
├── document.html                     # Document detail + review actions
├── contexts.html                     # Context library management page
├── duplicate.html                    # (legacy) Side-by-side duplicate resolution
├── errors.html                       # (legacy) Error list
├── review.html                       # (legacy) Review inbox
└── partials/
    ├── document_table.html           # Document table body (HTMX swap target)
    ├── document_row.html             # Single row in dashboard table
    ├── ocr_detail.html               # Inline OCR/stage detail expansion
    ├── status_counts.html            # Status count badges (HTMX polling)
    ├── context_library.html          # Context picker dropdown
    └── context_library_manage.html   # Context library CRUD panel
```

All partial templates are designed for HTMX out-of-band swaps — the full-page templates include them by reference, and HTMX actions target them directly for updates without reloading the full page.
