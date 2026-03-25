from __future__ import annotations

from dataclasses import replace
from datetime import datetime, timezone

from fastapi import APIRouter, Request
from fastapi.responses import HTMLResponse
from fastapi.templating import Jinja2Templates

from core.services import review as review_service

router = APIRouter()
templates = Jinja2Templates(directory="ui/templates")

_STATE_ORDER = ["pending", "running", "waiting", "error", "done"]


def _build_review_items(docs: list, config) -> list[dict]:
    items = []
    for doc in docs:
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
            "llm_stage": prev,
            "input_text": input_text,
            "output_text": output_text,
            "clarification_requests": clarification_requests,
        })
    return items


@router.get("/", response_class=HTMLResponse)
async def dashboard(request: Request):
    db = request.app.state.db
    docs = await db.list_documents()
    counts = await db.status_counts()
    return templates.TemplateResponse(
        "dashboard.html",
        {"request": request, "docs": docs, "counts": counts, "state_order": _STATE_ORDER},
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
    docs = await request.app.state.db.list_documents()
    return templates.TemplateResponse(
        "partials/document_table.html",
        {"request": request, "docs": docs, "state_order": _STATE_ORDER},
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
    # Stages that have been run (have data in stage_data, excluding _ingest)
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

    # Find stage index and clear data for this stage and everything after
    stage_names = [s.name for s in config.stages]
    replay_idx = stage_names.index(stage_name)
    stage_data = {k: v for k, v in doc.stage_data.items()
                  if k == "_ingest" or k not in stage_names[replay_idx:]}

    now_str = datetime.now(timezone.utc).isoformat()
    updated = replace(doc, current_stage=stage_name, stage_state="pending",
                      stage_data=stage_data, updated_at=now_str)
    await db.update(updated)
    await db.append_event(doc_id, stage_name, "replayed", now_str)
    docs = await db.list_documents()
    return templates.TemplateResponse(
        "partials/document_table.html",
        {"request": request, "docs": docs, "state_order": _STATE_ORDER},
    )


@router.post("/api/documents/{doc_id}/retry", response_class=HTMLResponse)
async def document_retry(request: Request, doc_id: str):
    """Reset an errored document back to pending on its current stage."""
    db = request.app.state.db
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    now_str = datetime.now(timezone.utc).isoformat()
    updated = replace(doc, stage_state="pending", updated_at=now_str)
    await db.update(updated)
    await db.append_event(doc_id, doc.current_stage, "retried", now_str)
    docs = await db.list_documents()
    return templates.TemplateResponse(
        "partials/document_table.html",
        {"request": request, "docs": docs, "state_order": _STATE_ORDER},
    )


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
    await review_service.reject_with_clarifications(
        doc, stage_name, clarification_responses, config, db,
        datetime.now(timezone.utc).isoformat(),
    )
    items = _build_review_items(await db.get_waiting(), config)
    return templates.TemplateResponse(
        "partials/review_item.html", {"request": request, "review_items": items}
    )


@router.get("/healthz")
async def healthz():
    return {"status": "ok"}
