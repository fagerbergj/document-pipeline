from __future__ import annotations

import difflib
import html
import json
from dataclasses import replace
from datetime import datetime, timezone
from pathlib import Path

import asyncio
import json as _json

from fastapi import APIRouter, Request
from fastapi.responses import HTMLResponse, RedirectResponse, StreamingResponse
from fastapi.templating import Jinja2Templates

from core.services import review as review_service

_CONTEXT_LIBRARY_PATH = Path("prompts/context_library.json")


def _load_context_library() -> list[dict]:
    if not _CONTEXT_LIBRARY_PATH.exists():
        return []
    try:
        return json.loads(_CONTEXT_LIBRARY_PATH.read_text(encoding="utf-8"))
    except Exception:
        return []


def _save_context_library(entries: list[dict]) -> None:
    _CONTEXT_LIBRARY_PATH.write_text(
        json.dumps(entries, indent=2, ensure_ascii=False), encoding="utf-8"
    )

router = APIRouter()
templates = Jinja2Templates(directory="ui/templates")

_STATE_ORDER = ["pending", "running", "waiting", "error", "done"]


def _diff_html(before: str, after: str) -> str:
    """Return a line-level diff as an HTML fragment with added/removed highlights."""
    before_lines = before.splitlines()
    after_lines = after.splitlines()
    matcher = difflib.SequenceMatcher(None, before_lines, after_lines, autojunk=False)
    parts = []
    for op, i1, i2, j1, j2 in matcher.get_opcodes():
        if op == "equal":
            for line in before_lines[i1:i2]:
                parts.append(f'<div class="diff-eq">{html.escape(line) or "&nbsp;"}</div>')
        elif op in ("replace", "delete"):
            for line in before_lines[i1:i2]:
                parts.append(f'<div class="diff-del">- {html.escape(line)}</div>')
        if op in ("replace", "insert"):
            for line in after_lines[j1:j2]:
                parts.append(f'<div class="diff-add">+ {html.escape(line)}</div>')
    return "\n".join(parts)


def _build_review_items(docs: list, config) -> list[dict]:
    items = []
    for doc in docs:
        stage_def = config.get_stage(doc.current_stage)

        # Needs-context: parked on an llm_text stage awaiting document_context
        if stage_def and stage_def.require_context:
            items.append({
                "doc": doc,
                "needs_context": True,
                "document_context": doc.stage_data.get("_ingest", {}).get("document_context", ""),
                "llm_stage": stage_def,
                "input_text": "",
                "output_text": "",
                "diff_html": "",
                "clarification_requests": [],
            })
            continue

        # Normal manual_review: show prev llm stage output for approval
        prev = config.prev_stage(doc.current_stage)
        if prev is None:
            continue
        llm_data = doc.stage_data.get(prev.name, {})
        input_text = ""
        if prev.input:
            for _, sdata in doc.stage_data.items():
                if isinstance(sdata, dict) and prev.input in sdata:
                    input_text = sdata[prev.input]
                    break
        output_text = llm_data.get(prev.output, "") if prev.output else ""
        clarification_requests = llm_data.get("clarification_requests", [])
        items.append({
            "doc": doc,
            "needs_context": False,
            "llm_stage": prev,
            "input_text": input_text,
            "output_text": output_text,
            "diff_html": _diff_html(input_text, output_text),
            "clarification_requests": clarification_requests,
            "document_context": "",
        })
    return items


def _table_response(request, docs, config):
    return templates.TemplateResponse(
        "partials/document_table.html",
        {"request": request, "docs": docs, "state_order": _STATE_ORDER,
         "needs_context_ids": _needs_context_ids(docs, config)},
    )


def _needs_context_ids(docs: list, config) -> set:
    """Return set of doc IDs that are blocked waiting for document context."""
    out = set()
    for doc in docs:
        stage_def = config.get_stage(doc.current_stage)
        if (
            stage_def
            and stage_def.require_context
            and not doc.stage_data.get("_ingest", {}).get("document_context")
        ):
            out.add(doc.id)
    return out


@router.get("/", response_class=HTMLResponse)
async def dashboard(request: Request):
    db = request.app.state.db
    config = request.app.state.pipeline
    docs = await db.list_documents()
    counts = await db.status_counts()
    return templates.TemplateResponse(
        "dashboard.html",
        {"request": request, "docs": docs, "counts": counts, "state_order": _STATE_ORDER,
         "needs_context_ids": _needs_context_ids(docs, config)},
    )


@router.get("/api/counts", response_class=HTMLResponse)
async def status_counts(request: Request):
    """HTMX target: refreshes only the status count cards."""
    counts = await request.app.state.db.status_counts()
    return templates.TemplateResponse(
        "partials/status_counts.html", {"request": request, "counts": counts}
    )


@router.get("/api/documents", response_class=HTMLResponse)
async def documents_table(request: Request):
    """HTMX target: refreshes the document table body."""
    db = request.app.state.db
    config = request.app.state.pipeline
    docs = await db.list_documents()
    return templates.TemplateResponse(
        "partials/document_table.html",
        {"request": request, "docs": docs, "state_order": _STATE_ORDER,
         "needs_context_ids": _needs_context_ids(docs, config)},
    )


@router.get("/api/documents/{doc_id}/ocr", response_class=HTMLResponse)
async def document_ocr(request: Request, doc_id: str):
    """Returns the OCR text snippet for inline expansion."""
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    ocr_text = (doc.stage_data.get("ocr") or {}).get("ocr_raw", "(no OCR text yet)")
    completed_stages = [s for s in config.stages if s.name in doc.stage_data]
    return templates.TemplateResponse(
        "partials/ocr_detail.html",
        {"request": request, "doc": doc, "ocr_text": ocr_text,
         "completed_stages": completed_stages},
    )


@router.post("/api/documents/{doc_id}/replay/{stage_name}", response_class=HTMLResponse)
async def document_replay(request: Request, doc_id: str, stage_name: str):
    """Reset a document to replay from the given stage, clearing downstream stage_data."""
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    if config.get_stage(stage_name) is None:
        return HTMLResponse("<em>Unknown stage</em>", status_code=400)

    stage_names = [s.name for s in config.stages]
    replay_idx = stage_names.index(stage_name)
    stage_data = {k: v for k, v in doc.stage_data.items()
                  if k == "_ingest" or k not in stage_names[replay_idx:]}

    now_str = datetime.now(timezone.utc).isoformat()
    updated = replace(doc, current_stage=stage_name, stage_state="pending",
                      stage_data=stage_data, updated_at=now_str)
    await db.update(updated)
    await db.append_event(doc_id, stage_name, "replayed", now_str)
    return _table_response(request, await db.list_documents(), config)


@router.post("/api/documents/{doc_id}/stop", response_class=HTMLResponse)
async def document_stop(request: Request, doc_id: str):
    """Stop a running document by setting it to error state."""
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    now_str = datetime.now(timezone.utc).isoformat()
    await db.update(replace(doc, stage_state="error", updated_at=now_str))
    await db.append_event(doc_id, doc.current_stage, "stopped", now_str)
    return _table_response(request, await db.list_documents(), config)


@router.post("/api/documents/{doc_id}/title", response_class=HTMLResponse)
async def document_set_title(request: Request, doc_id: str):
    """Update the document title."""
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    form = await request.form()
    new_title = form.get("title", "").strip()
    if not new_title:
        return HTMLResponse("<em>Title required</em>", status_code=400)
    now_str = datetime.now(timezone.utc).isoformat()
    await db.update(replace(doc, title=new_title, updated_at=now_str))
    return _table_response(request, await db.list_documents(), config)


@router.post("/api/documents/{doc_id}/retry", response_class=HTMLResponse)
async def document_retry(request: Request, doc_id: str):
    """Reset an errored document back to pending on its current stage."""
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    now_str = datetime.now(timezone.utc).isoformat()
    updated = replace(doc, stage_state="pending", updated_at=now_str)
    await db.update(updated)
    await db.append_event(doc_id, doc.current_stage, "retried", now_str)
    return _table_response(request, await db.list_documents(), config)


@router.get("/review", response_class=HTMLResponse)
async def review_inbox(request: Request):
    db = request.app.state.db
    config = request.app.state.pipeline
    docs = await db.get_waiting()
    items = _build_review_items(docs, config)
    return templates.TemplateResponse("review.html", {"request": request, "review_items": items})


@router.get("/api/review", response_class=HTMLResponse)
async def review_partial(request: Request):
    db = request.app.state.db
    config = request.app.state.pipeline
    docs = await db.get_waiting()
    items = _build_review_items(docs, config)
    return templates.TemplateResponse(
        "partials/review_item.html", {"request": request, "review_items": items}
    )


@router.post("/api/review/{doc_id}/approve", response_class=HTMLResponse)
async def review_approve(request: Request, doc_id: str):
    db, config = request.app.state.db, request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)

    # Save any manual edits to the output field before approving
    form = await request.form()
    edited = form.get("edited_text", "").strip()
    if edited:
        prev = config.prev_stage(doc.current_stage)
        if prev and prev.output:
            stage_data = dict(doc.stage_data)
            entry = dict(stage_data.get(prev.name, {}))
            entry[prev.output] = edited
            stage_data[prev.name] = entry
            doc = replace(doc, stage_data=stage_data)
            await db.update(doc)

    await review_service.approve(doc, config, db, datetime.now(timezone.utc).isoformat())
    items = _build_review_items(await db.get_waiting(), config)
    return templates.TemplateResponse(
        "partials/review_item.html", {"request": request, "review_items": items}
    )


@router.post("/api/review/{doc_id}/reject", response_class=HTMLResponse)
async def review_reject(request: Request, doc_id: str):
    db, config = request.app.state.db, request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    await review_service.reject(doc, config, db, datetime.now(timezone.utc).isoformat())
    items = _build_review_items(await db.get_waiting(), config)
    return templates.TemplateResponse(
        "partials/review_item.html", {"request": request, "review_items": items}
    )


@router.post("/api/review/{doc_id}/clarify", response_class=HTMLResponse)
async def review_clarify_submit(request: Request, doc_id: str):
    db, config = request.app.state.db, request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    prev = config.prev_stage(doc.current_stage)
    if prev is None:
        return HTMLResponse("<em>No previous stage</em>", status_code=400)
    stage_name = prev.name
    form = await request.form()
    existing_requests = (doc.stage_data.get(stage_name) or {}).get("clarification_requests", [])
    clarification_responses = [
        {"segment": req["segment"], "answer": form.get(f"answer_{i}", "").strip()}
        for i, req in enumerate(existing_requests)
    ]
    free_prompt = form.get("free_prompt", "").strip()
    document_context = form.get("document_context", "").strip()

    # Save updated document_context if provided
    if document_context:
        stage_data = dict(doc.stage_data)
        ingest = dict(stage_data.get("_ingest", {}))
        ingest["document_context"] = document_context
        stage_data["_ingest"] = ingest
        doc = replace(doc, stage_data=stage_data)
        await db.update(doc)

    await review_service.reject_with_clarifications(
        doc, stage_name, clarification_responses, config, db,
        datetime.now(timezone.utc).isoformat(),
        free_prompt=free_prompt,
    )
    items = _build_review_items(await db.get_waiting(), config)
    return templates.TemplateResponse(
        "partials/review_item.html", {"request": request, "review_items": items}
    )


@router.get("/api/context-library", response_class=HTMLResponse)
async def context_library_get(request: Request):
    entries = _load_context_library()
    return templates.TemplateResponse(
        "partials/context_library.html", {"request": request, "entries": entries}
    )


@router.post("/api/context-library", response_class=HTMLResponse)
async def context_library_save(request: Request):
    form = await request.form()
    name = form.get("library_name", "").strip()
    text = form.get("library_text", "").strip()
    if name and text:
        entries = _load_context_library()
        for e in entries:
            if e["name"] == name:
                e["text"] = text
                break
        else:
            entries.append({"name": name, "text": text})
        _save_context_library(entries)
    return templates.TemplateResponse(
        "partials/context_library.html",
        {"request": request, "entries": _load_context_library()},
    )


@router.post("/api/documents/{doc_id}/set-context", response_class=HTMLResponse)
async def document_set_context(request: Request, doc_id: str):
    """Save document_context into stage_data._ingest and reset stage to pending."""
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    form = await request.form()
    document_context = form.get("document_context", "").strip()

    stage_data = dict(doc.stage_data)
    ingest = dict(stage_data.get("_ingest", {}))
    ingest["document_context"] = document_context
    stage_data["_ingest"] = ingest

    now_str = datetime.now(timezone.utc).isoformat()
    updated = replace(doc, stage_data=stage_data, stage_state="pending", updated_at=now_str)
    await db.update(updated)
    await db.append_event(doc_id, doc.current_stage, "context_set", now_str)

    items = _build_review_items(await db.get_waiting(), config)
    return templates.TemplateResponse(
        "partials/review_item.html", {"request": request, "review_items": items}
    )


@router.get("/contexts", response_class=HTMLResponse)
async def contexts_page(request: Request):
    entries = _load_context_library()
    return templates.TemplateResponse(
        "contexts.html", {"request": request, "entries": entries}
    )


@router.get("/api/context-library/manage", response_class=HTMLResponse)
async def context_library_manage_get(request: Request):
    return templates.TemplateResponse(
        "partials/context_library_manage.html",
        {"request": request, "entries": _load_context_library()},
    )


@router.post("/api/context-library/manage", response_class=HTMLResponse)
async def context_library_manage_save(request: Request):
    form = await request.form()
    name = form.get("library_name", "").strip()
    text = form.get("library_text", "").strip()
    if name and text:
        entries = _load_context_library()
        for e in entries:
            if e["name"] == name:
                e["text"] = text
                break
        else:
            entries.append({"name": name, "text": text})
        _save_context_library(entries)
    return templates.TemplateResponse(
        "partials/context_library_manage.html",
        {"request": request, "entries": _load_context_library()},
    )


@router.post("/api/context-library/delete", response_class=HTMLResponse)
async def context_library_delete(request: Request):
    form = await request.form()
    name = form.get("name", "").strip()
    if name:
        entries = [e for e in _load_context_library() if e["name"] != name]
        _save_context_library(entries)
    return templates.TemplateResponse(
        "partials/context_library_manage.html",
        {"request": request, "entries": _load_context_library()},
    )


def _doc_redirect(doc_id: str):
    return RedirectResponse(f"/documents/{doc_id}", status_code=303)


def _build_doc_view(doc, config) -> dict:
    stage_def = config.get_stage(doc.current_stage)
    document_context = doc.stage_data.get("_ingest", {}).get("document_context", "")

    # context_required: doc is parked waiting for context before it can run
    context_required = (
        doc.stage_state in ("waiting", "pending")
        and stage_def is not None
        and stage_def.require_context
        and not document_context
    )

    review = None
    if doc.stage_state == "waiting" and not (stage_def and stage_def.require_context):
        prev = config.prev_stage(doc.current_stage)
        if prev:
            llm_data = doc.stage_data.get(prev.name, {})
            input_text = ""
            if prev.input:
                for _, sdata in doc.stage_data.items():
                    if isinstance(sdata, dict) and prev.input in sdata:
                        input_text = sdata[prev.input]
                        break
            output_text = llm_data.get(prev.output, "") if prev.output else ""
            review = {
                "needs_context": False,
                "llm_stage": prev,
                "input_text": input_text,
                "output_text": output_text,
                "diff_html": _diff_html(input_text, output_text),
                "clarification_requests": llm_data.get("clarification_requests", []),
            }

    stage_displays = []
    for s in config.stages:
        if s.type == "manual_review":
            continue
        sdata = doc.stage_data.get(s.name)
        if not sdata:
            continue
        fields = {}
        if s.output and s.output in sdata:
            fields[s.output] = sdata[s.output]
        if s.outputs:
            for o in s.outputs:
                field = o.get("field")
                if field and field in sdata:
                    fields[field] = sdata[field]
        if not fields and s.type == "computer_vision" and "ocr_raw" in sdata:
            fields["ocr_raw"] = sdata["ocr_raw"]
        if fields:
            stage_displays.append({"name": s.name, "type": s.type, "fields": fields})

    replay_stages = [s for s in config.stages if s.name in doc.stage_data]

    return {
        "document_context": document_context,
        "context_required": context_required,
        "stage_displays": stage_displays,
        "replay_stages": replay_stages,
        "review": review,
    }


@router.get("/documents/{doc_id}", response_class=HTMLResponse)
async def document_page(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    return templates.TemplateResponse(
        "document.html",
        {"request": request, "doc": doc, **_build_doc_view(doc, config)},
    )


@router.post("/documents/{doc_id}/title")
async def doc_set_title(request: Request, doc_id: str):
    db = request.app.state.db
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    form = await request.form()
    new_title = form.get("title", "").strip()
    if new_title:
        now_str = datetime.now(timezone.utc).isoformat()
        await db.update(replace(doc, title=new_title, updated_at=now_str))
    return _doc_redirect(doc_id)


@router.post("/documents/{doc_id}/context")
async def doc_save_context(request: Request, doc_id: str):
    """Save document context without changing stage state."""
    db = request.app.state.db
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    form = await request.form()
    document_context = form.get("document_context", "").strip()
    stage_data = dict(doc.stage_data)
    ingest = dict(stage_data.get("_ingest", {}))
    ingest["document_context"] = document_context
    stage_data["_ingest"] = ingest
    now_str = datetime.now(timezone.utc).isoformat()
    await db.update(replace(doc, stage_data=stage_data, updated_at=now_str))
    return _doc_redirect(doc_id)


@router.post("/documents/{doc_id}/set-context")
async def doc_set_context_and_run(request: Request, doc_id: str):
    """Save context and reset stage to pending so the worker picks it up."""
    db = request.app.state.db
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    form = await request.form()
    document_context = form.get("document_context", "").strip()
    stage_data = dict(doc.stage_data)
    ingest = dict(stage_data.get("_ingest", {}))
    ingest["document_context"] = document_context
    stage_data["_ingest"] = ingest
    now_str = datetime.now(timezone.utc).isoformat()
    updated = replace(doc, stage_data=stage_data, stage_state="pending", updated_at=now_str)
    await db.update(updated)
    await db.append_event(doc_id, doc.current_stage, "context_set", now_str)
    return _doc_redirect(doc_id)


@router.post("/documents/{doc_id}/stop")
async def doc_stop(request: Request, doc_id: str):
    db = request.app.state.db
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    now_str = datetime.now(timezone.utc).isoformat()
    await db.update(replace(doc, stage_state="error", updated_at=now_str))
    await db.append_event(doc_id, doc.current_stage, "stopped", now_str)
    return _doc_redirect(doc_id)


@router.post("/documents/{doc_id}/retry")
async def doc_retry(request: Request, doc_id: str):
    db = request.app.state.db
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    now_str = datetime.now(timezone.utc).isoformat()
    await db.update(replace(doc, stage_state="pending", updated_at=now_str))
    await db.append_event(doc_id, doc.current_stage, "retried", now_str)
    return _doc_redirect(doc_id)


@router.post("/documents/{doc_id}/replay/{stage_name}")
async def doc_replay(request: Request, doc_id: str, stage_name: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    stage_names = [s.name for s in config.stages]
    if stage_name not in stage_names:
        return HTMLResponse("<em>Unknown stage</em>", status_code=400)
    replay_idx = stage_names.index(stage_name)
    stage_data = {k: v for k, v in doc.stage_data.items()
                  if k == "_ingest" or k not in stage_names[replay_idx:]}
    now_str = datetime.now(timezone.utc).isoformat()
    updated = replace(doc, current_stage=stage_name, stage_state="pending",
                      stage_data=stage_data, updated_at=now_str)
    await db.update(updated)
    await db.append_event(doc_id, stage_name, "replayed", now_str)
    return _doc_redirect(doc_id)


@router.post("/documents/{doc_id}/approve")
async def doc_approve(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    form = await request.form()
    edited = form.get("edited_text", "").strip()
    if edited:
        prev = config.prev_stage(doc.current_stage)
        if prev and prev.output:
            stage_data = dict(doc.stage_data)
            entry = dict(stage_data.get(prev.name, {}))
            entry[prev.output] = edited
            stage_data[prev.name] = entry
            doc = replace(doc, stage_data=stage_data)
            await db.update(doc)
    await review_service.approve(doc, config, db, datetime.now(timezone.utc).isoformat())
    return _doc_redirect(doc_id)


@router.post("/documents/{doc_id}/reject")
async def doc_reject(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    await review_service.reject(doc, config, db, datetime.now(timezone.utc).isoformat())
    return _doc_redirect(doc_id)


@router.post("/documents/{doc_id}/clarify")
async def doc_clarify(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    prev = config.prev_stage(doc.current_stage)
    if prev is None:
        return HTMLResponse("<em>No previous stage</em>", status_code=400)
    stage_name = prev.name
    form = await request.form()
    existing_requests = (doc.stage_data.get(stage_name) or {}).get("clarification_requests", [])
    clarification_responses = [
        {"segment": req["segment"], "answer": form.get(f"answer_{i}", "").strip()}
        for i, req in enumerate(existing_requests)
    ]
    free_prompt = form.get("free_prompt", "").strip()
    document_context = form.get("document_context", "").strip()
    if document_context:
        stage_data = dict(doc.stage_data)
        ingest = dict(stage_data.get("_ingest", {}))
        ingest["document_context"] = document_context
        stage_data["_ingest"] = ingest
        doc = replace(doc, stage_data=stage_data)
        await db.update(doc)
    await review_service.reject_with_clarifications(
        doc, stage_name, clarification_responses, config, db,
        datetime.now(timezone.utc).isoformat(),
        free_prompt=free_prompt,
    )
    return _doc_redirect(doc_id)


@router.get("/api/documents/{doc_id}/stream")
async def doc_token_stream(request: Request, doc_id: str):
    """SSE endpoint: streams LLM tokens while the document is running, then sends 'done'."""
    from adapters.outbound import streams as _streams
    db = request.app.state.db

    async def generate():
        q = _streams.get_queue(doc_id)
        last_state_check = asyncio.get_event_loop().time()
        while True:
            if await request.is_disconnected():
                break
            try:
                item = await asyncio.wait_for(q.get(), timeout=1.5)
                if item is None:
                    yield f"event: done\ndata: {{}}\n\n"
                    break
                yield f"event: token\ndata: {_json.dumps(item)}\n\n"
            except asyncio.TimeoutError:
                # Periodic fallback: if doc is no longer running, close stream
                now = asyncio.get_event_loop().time()
                if now - last_state_check > 4.0:
                    last_state_check = now
                    doc = await db.get(doc_id)
                    if doc and doc.stage_state != "running":
                        yield f"event: done\ndata: {{}}\n\n"
                        break
                yield ": ping\n\n"

    return StreamingResponse(
        generate(),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


@router.get("/healthz")
async def healthz():
    return {"status": "ok"}
