from __future__ import annotations

import difflib
import html
import json
from dataclasses import replace
from datetime import datetime, timezone
from pathlib import Path

import asyncio
import json as _json

from typing import Optional

from fastapi import APIRouter, Query, Request
from fastapi.responses import HTMLResponse, RedirectResponse, StreamingResponse
from fastapi.templating import Jinja2Templates

from core.services import review as review_service

_CTX_LIB_KEY = "context_library"


async def _load_context_library(db) -> list[dict]:
    raw = await db.kv_get(_CTX_LIB_KEY)
    if not raw:
        return []
    try:
        return json.loads(raw)
    except Exception:
        return []


async def _save_context_library(db, entries: list[dict]) -> None:
    await db.kv_set(_CTX_LIB_KEY, json.dumps(entries, ensure_ascii=False))

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
async def dashboard(
    request: Request,
    stage: Optional[str] = Query(default=None),
    state: Optional[str] = Query(default=None),
    sort: str = Query(default="created_desc"),
):
    db = request.app.state.db
    config = request.app.state.pipeline
    docs = await db.list_documents(stage=stage, state=state, sort=sort)
    counts = await db.status_counts()
    all_stages = [s.name for s in config.stages]
    return templates.TemplateResponse(
        "dashboard.html",
        {"request": request, "docs": docs, "counts": counts, "state_order": _STATE_ORDER,
         "needs_context_ids": _needs_context_ids(docs, config),
         "all_stages": all_stages,
         "filter_stage": stage or "", "filter_state": state or "", "filter_sort": sort},
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


@router.get("/api/context-library", response_class=HTMLResponse)
async def context_library_get(request: Request):
    db = request.app.state.db
    entries = await _load_context_library(db)
    return templates.TemplateResponse(
        "partials/context_library.html", {"request": request, "entries": entries}
    )


@router.post("/api/context-library", response_class=HTMLResponse)
async def context_library_save(request: Request):
    db = request.app.state.db
    form = await request.form()
    name = form.get("library_name", "").strip()
    text = form.get("library_text", "").strip()
    if name and text:
        entries = await _load_context_library(db)
        for e in entries:
            if e["name"] == name:
                e["text"] = text
                break
        else:
            entries.append({"name": name, "text": text})
        await _save_context_library(db, entries)
    return templates.TemplateResponse(
        "partials/context_library.html",
        {"request": request, "entries": await _load_context_library(db)},
    )


@router.get("/contexts", response_class=HTMLResponse)
async def contexts_page(request: Request):
    db = request.app.state.db
    entries = await _load_context_library(db)
    return templates.TemplateResponse(
        "contexts.html", {"request": request, "entries": entries}
    )


@router.get("/api/context-library/manage", response_class=HTMLResponse)
async def context_library_manage_get(request: Request):
    db = request.app.state.db
    return templates.TemplateResponse(
        "partials/context_library_manage.html",
        {"request": request, "entries": await _load_context_library(db)},
    )


@router.post("/api/context-library/manage", response_class=HTMLResponse)
async def context_library_manage_save(request: Request):
    db = request.app.state.db
    form = await request.form()
    name = form.get("library_name", "").strip()
    text = form.get("library_text", "").strip()
    if name and text:
        entries = await _load_context_library(db)
        for e in entries:
            if e["name"] == name:
                e["text"] = text
                break
        else:
            entries.append({"name": name, "text": text})
        await _save_context_library(db, entries)
    return templates.TemplateResponse(
        "partials/context_library_manage.html",
        {"request": request, "entries": await _load_context_library(db)},
    )


@router.post("/api/context-library/delete", response_class=HTMLResponse)
async def context_library_delete(request: Request):
    db = request.app.state.db
    form = await request.form()
    name = form.get("name", "").strip()
    if name:
        entries = [e for e in await _load_context_library(db) if e["name"] != name]
        await _save_context_library(db, entries)
    return templates.TemplateResponse(
        "partials/context_library_manage.html",
        {"request": request, "entries": await _load_context_library(db)},
    )


def _doc_redirect(doc_id: str):
    return RedirectResponse(f"/documents/{doc_id}", status_code=303)


def _build_doc_view(doc, config) -> dict:
    stage_def = config.get_stage(doc.current_stage)
    document_context = doc.stage_data.get("_ingest", {}).get("document_context", "")

    # Determine if the stage has LLM output already (ran but parked) vs waiting to start
    sdata = doc.stage_data.get(doc.current_stage, {}) if doc.current_stage else {}
    has_llm_output = bool(
        sdata and stage_def and (
            (stage_def.output and sdata.get(stage_def.output)) or
            (stage_def.outputs and any(sdata.get(o.get("field", "")) for o in (stage_def.outputs or [])))
        )
    )

    # context_required: waiting to START (no output yet, start condition not met)
    start_if = (stage_def.start_if or {}) if stage_def else {}
    context_required = (
        doc.stage_state in ("waiting", "pending")
        and stage_def is not None
        and not has_llm_output
        and (stage_def.require_context or start_if.get("context_provided"))
        and not document_context
    )

    # review: ran but parked for human review (has output, waiting state)
    review = None
    if doc.stage_state == "waiting" and has_llm_output and stage_def and stage_def.type == "llm_text":
        input_text = ""
        if stage_def.input:
            for _, sd in doc.stage_data.items():
                if isinstance(sd, dict) and stage_def.input in sd:
                    input_text = sd[stage_def.input]
                    break
        if stage_def.output:
            output_text = sdata.get(stage_def.output, "")
        elif stage_def.outputs:
            parts = []
            for o in stage_def.outputs:
                field = o.get("field", "")
                val = sdata.get(field)
                if val is not None:
                    parts.append(f"{field}:\n{val if isinstance(val, str) else _json.dumps(val, indent=2)}")
            output_text = "\n\n".join(parts)
        else:
            output_text = ""
        confidence = sdata.get("confidence", "")
        review = {
            "llm_stage": stage_def,
            "input_text": input_text,
            "output_text": output_text,
            "diff_html": _diff_html(input_text, output_text),
            "clarification_requests": sdata.get("clarification_requests", []),
            "confidence": confidence,
            "qa_rounds": len(sdata.get("qa_history", [])),
        }

    stage_displays = []
    for s in config.stages:
        if s.type in ("manual_review",):
            continue
        sd = doc.stage_data.get(s.name)
        if not sd:
            continue
        fields = {}
        if s.output and s.output in sd:
            fields[s.output] = sd[s.output]
        if s.outputs:
            for o in s.outputs:
                field = o.get("field")
                if field and field in sd:
                    fields[field] = sd[field]
        if not fields and s.type == "computer_vision" and "ocr_raw" in sd:
            fields["ocr_raw"] = sd["ocr_raw"]
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
    stage_def = config.get_stage(doc.current_stage)
    if edited and stage_def and stage_def.output:
        stage_data = dict(doc.stage_data)
        entry = dict(stage_data.get(stage_def.name, {}))
        entry[stage_def.output] = edited
        stage_data[stage_def.name] = entry
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
    stage_name = doc.current_stage
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
        doc, clarification_responses, config, db,
        datetime.now(timezone.utc).isoformat(),
        free_prompt=free_prompt,
    )
    return _doc_redirect(doc_id)


@router.get("/api/documents/{doc_id}/stream")
async def doc_token_stream(request: Request, doc_id: str):
    """SSE endpoint: streams LLM tokens while the document is running, then sends 'done'."""
    from adapters.outbound import streams as _streams
    db = request.app.state.db

    doc = await db.get(doc_id)
    initial_state = f"{doc.current_stage}:{doc.stage_state}" if doc else ""

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
                # Poll doc state every 3s — reload on any state change
                now = asyncio.get_event_loop().time()
                if now - last_state_check > 3.0:
                    last_state_check = now
                    current = await db.get(doc_id)
                    current_state = f"{current.current_stage}:{current.stage_state}" if current else ""
                    if current_state != initial_state:
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
