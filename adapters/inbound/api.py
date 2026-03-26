from __future__ import annotations

import asyncio
import json as _json
import os as _os
from dataclasses import replace
from datetime import datetime, timezone
from typing import Optional

from fastapi import APIRouter, Request, Query
from fastapi.responses import FileResponse, JSONResponse, StreamingResponse

from core.services import review as review_service

router = APIRouter(prefix="/api/v1")

_CTX_LIB_KEY = "context_library"


async def _load_context_library(db) -> list[dict]:
    raw = await db.kv_get(_CTX_LIB_KEY)
    if not raw:
        return []
    try:
        return _json.loads(raw)
    except Exception:
        return []


async def _save_context_library(db, entries: list[dict]) -> None:
    await db.kv_set(_CTX_LIB_KEY, _json.dumps(entries, ensure_ascii=False))


def _build_doc_detail(doc, config, events: list | None = None) -> dict:
    stage_def = config.get_stage(doc.current_stage)
    document_context = doc.stage_data.get("_ingest", {}).get("document_context", "")

    sdata = doc.stage_data.get(doc.current_stage, {}) if doc.current_stage else {}
    has_llm_output = bool(
        sdata and stage_def and (
            (stage_def.output and sdata.get(stage_def.output)) or
            (stage_def.outputs and any(sdata.get(o.get("field", "")) for o in (stage_def.outputs or [])))
        )
    )

    start_if = (stage_def.start_if or {}) if stage_def else {}
    context_required = (
        doc.stage_state in ("waiting", "pending")
        and stage_def is not None
        and not has_llm_output
        and (stage_def.require_context or start_if.get("context_provided"))
        and not document_context
    )

    # needs_context for summary list
    needs_context = (
        doc.stage_state in ("waiting", "pending")
        and stage_def is not None
        and (start_if.get("context_provided") or stage_def.require_context)
        and not document_context
    )

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

        is_single_output = bool(stage_def.output)
        review = {
            "stage_name": doc.current_stage,
            "input_field": stage_def.input,
            "output_field": stage_def.output if stage_def.output else None,
            "input_text": input_text,
            "output_text": output_text,
            "is_single_output": is_single_output,
            "confidence": sdata.get("confidence", ""),
            "qa_rounds": len(sdata.get("qa_history", [])),
            "clarification_requests": sdata.get("clarification_requests", []),
            "context_updates": sdata.get("context_updates", ""),
        }

    stage_displays = []
    for s in config.stages:
        if s.type in ("manual_review",):
            continue
        sd = doc.stage_data.get(s.name)
        if not sd:
            continue
        fields: dict[str, str] = {}
        if s.output and s.output in sd:
            val = sd[s.output]
            fields[s.output] = val if isinstance(val, str) else _json.dumps(val, ensure_ascii=False)
        if s.outputs:
            for o in s.outputs:
                field = o.get("field")
                if field and field in sd:
                    val = sd[field]
                    fields[field] = val if isinstance(val, str) else _json.dumps(val, ensure_ascii=False)
        if not fields and s.type == "computer_vision" and "ocr_raw" in sd:
            val = sd["ocr_raw"]
            fields["ocr_raw"] = val if isinstance(val, str) else _json.dumps(val, ensure_ascii=False)
        if fields:
            stage_displays.append({"name": s.name, "fields": fields})

    stage_names = [s.name for s in config.stages]
    current_idx = stage_names.index(doc.current_stage) if doc.current_stage in stage_names else -1
    replay_stages = [
        {"name": s.name}
        for s in config.stages
        if s.name in doc.stage_data and (current_idx == -1 or stage_names.index(s.name) < current_idx)
    ]

    return {
        "id": doc.id,
        "title": doc.title,
        "current_stage": doc.current_stage,
        "stage_state": doc.stage_state,
        "created_at": doc.created_at,
        "updated_at": doc.updated_at,
        "document_context": document_context,
        "context_required": context_required,
        "stage_displays": stage_displays,
        "review": review,
        "replay_stages": replay_stages,
        "needs_context": needs_context,
        "events": events or [],
        "has_image": bool(doc.png_path and _os.path.exists(doc.png_path)),
    }


def _doc_summary(doc, config) -> dict:
    stage_def = config.get_stage(doc.current_stage)
    document_context = doc.stage_data.get("_ingest", {}).get("document_context", "")
    start_if = (stage_def.start_if or {}) if stage_def else {}
    needs_context = (
        doc.stage_state in ("waiting", "pending")
        and stage_def is not None
        and (start_if.get("context_provided") or stage_def.require_context)
        and not document_context
    )
    return {
        "id": doc.id,
        "title": doc.title,
        "current_stage": doc.current_stage,
        "stage_state": doc.stage_state,
        "created_at": doc.created_at,
        "updated_at": doc.updated_at,
        "needs_context": needs_context,
    }


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


@router.get("/pipeline/stages")
async def get_stages(request: Request):
    config = request.app.state.pipeline
    return {"stages": [s.name for s in config.stages]}


@router.get("/counts")
async def get_counts(request: Request):
    db = request.app.state.db
    counts = await db.status_counts()
    return counts


@router.get("/documents")
async def list_documents(
    request: Request,
    stages: Optional[str] = Query(default=None),  # comma-separated
    states: Optional[str] = Query(default=None),  # comma-separated
    sort: str = Query(default="pipeline"),
):
    db = request.app.state.db
    config = request.app.state.pipeline
    stage_list = [s.strip() for s in stages.split(",")] if stages else None
    state_list = [s.strip() for s in states.split(",")] if states else None
    docs = await db.list_documents(stages=stage_list, states=state_list, sort=sort)
    return [_doc_summary(doc, config) for doc in docs]


@router.get("/documents/{doc_id}")
async def get_document(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    events = await db.get_events(doc_id)
    return _build_doc_detail(doc, config, events)


@router.get("/documents/{doc_id}/image")
async def get_document_image(request: Request, doc_id: str):
    db = request.app.state.db
    doc = await db.get(doc_id)
    if doc is None or not doc.png_path or not _os.path.exists(doc.png_path):
        return JSONResponse({"error": "not found"}, status_code=404)
    return FileResponse(doc.png_path, media_type="image/png")


@router.delete("/documents/{doc_id}")
async def delete_document(request: Request, doc_id: str):
    db = request.app.state.db
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    await db.delete(doc_id)
    return {"ok": True}


@router.post("/documents/{doc_id}/title")
async def update_title(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    body = await request.json()
    new_title = body.get("title", "").strip()
    if new_title:
        await db.update(replace(doc, title=new_title, updated_at=_now()))
    doc = await db.get(doc_id)
    return _build_doc_detail(doc, config)


@router.post("/documents/{doc_id}/context")
async def save_context(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    body = await request.json()
    document_context = body.get("document_context", "").strip()
    stage_data = dict(doc.stage_data)
    ingest = dict(stage_data.get("_ingest", {}))
    ingest["document_context"] = document_context
    stage_data["_ingest"] = ingest
    await db.update(replace(doc, stage_data=stage_data, updated_at=_now()))
    doc = await db.get(doc_id)
    return _build_doc_detail(doc, config)


@router.post("/documents/{doc_id}/set-context")
async def set_context_and_run(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    body = await request.json()
    document_context = body.get("document_context", "").strip()
    stage_data = dict(doc.stage_data)
    ingest = dict(stage_data.get("_ingest", {}))
    ingest["document_context"] = document_context
    stage_data["_ingest"] = ingest

    # Check if has LLM output
    stage_def = config.get_stage(doc.current_stage)
    sdata = stage_data.get(doc.current_stage, {})
    has_output = bool(sdata and stage_def and (
        (stage_def.output and sdata.get(stage_def.output)) or
        (stage_def.outputs and any(sdata.get(o.get("field", "")) for o in (stage_def.outputs or [])))
    ))
    new_state = doc.stage_state
    if doc.stage_state == "waiting" and not has_output:
        new_state = "pending"
    now_str = _now()
    updated = replace(doc, stage_data=stage_data, stage_state=new_state, updated_at=now_str)
    await db.update(updated)
    await db.append_event(doc_id, doc.current_stage, "context_set", now_str)
    doc = await db.get(doc_id)
    return _build_doc_detail(doc, config)


@router.post("/documents/{doc_id}/approve")
async def approve_document(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    body = await request.json()
    edited_text = body.get("edited_text", "").strip()
    stage_def = config.get_stage(doc.current_stage)
    if edited_text and stage_def and stage_def.output:
        stage_data = dict(doc.stage_data)
        entry = dict(stage_data.get(stage_def.name, {}))
        entry[stage_def.output] = edited_text
        stage_data[stage_def.name] = entry
        doc = replace(doc, stage_data=stage_data)
        await db.update(doc)
    updated = await review_service.approve(doc, config, db, _now())
    return _build_doc_detail(updated, config)


@router.post("/documents/{doc_id}/reject")
async def reject_document(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    updated = await review_service.reject(doc, config, db, _now())
    return _build_doc_detail(updated, config)


@router.post("/documents/{doc_id}/clarify")
async def clarify_document(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    body = await request.json()
    answers: dict = body.get("answers", {})
    free_prompt: str = body.get("free_prompt", "").strip()
    stage_name = doc.current_stage
    existing_requests = (doc.stage_data.get(stage_name) or {}).get("clarification_requests", [])
    clarification_responses = [
        {"segment": req["segment"], "answer": answers.get(str(i), "")}
        for i, req in enumerate(existing_requests)
    ]
    updated = await review_service.reject_with_clarifications(
        doc, clarification_responses, config, db, _now(), free_prompt=free_prompt
    )
    return _build_doc_detail(updated, config)



@router.delete("/documents/{doc_id}/errors")
async def clear_errors(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    await db.clear_errors(doc_id)
    events = await db.get_events(doc_id)
    return _build_doc_detail(doc, config, events)


@router.post("/documents/{doc_id}/stop")
async def stop_document(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    now_str = _now()
    updated = replace(doc, stage_state="error", updated_at=now_str)
    await db.update(updated)
    await db.append_event(doc_id, doc.current_stage, "stopped", now_str)
    return _build_doc_detail(updated, config)


@router.post("/documents/{doc_id}/retry")
async def retry_document(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    now_str = _now()
    updated = replace(doc, stage_state="pending", updated_at=now_str)
    await db.update(updated)
    await db.append_event(doc_id, doc.current_stage, "retried", now_str)
    return _build_doc_detail(updated, config)


@router.post("/documents/{doc_id}/replay/{stage_name}")
async def replay_document(request: Request, doc_id: str, stage_name: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    stage_names = [s.name for s in config.stages]
    if stage_name not in stage_names:
        return JSONResponse({"error": "unknown stage"}, status_code=400)
    replay_idx = stage_names.index(stage_name)
    stage_data = {k: v for k, v in doc.stage_data.items()
                  if k == "_ingest" or k not in stage_names[replay_idx:]}
    now_str = _now()
    updated = replace(doc, current_stage=stage_name, stage_state="pending",
                      stage_data=stage_data, updated_at=now_str)
    await db.update(updated)
    await db.append_event(doc_id, stage_name, "replayed", now_str)
    return _build_doc_detail(updated, config)


@router.get("/context-library")
async def get_context_library(request: Request):
    db = request.app.state.db
    entries = await _load_context_library(db)
    return entries


@router.post("/context-library")
async def save_context_entry(request: Request):
    db = request.app.state.db
    body = await request.json()
    name = body.get("name", "").strip()
    text = body.get("text", "").strip()
    if name and text:
        entries = await _load_context_library(db)
        for e in entries:
            if e["name"] == name:
                e["text"] = text
                break
        else:
            entries.append({"name": name, "text": text})
        await _save_context_library(db, entries)
    return await _load_context_library(db)


@router.delete("/context-library/{name}")
async def delete_context_entry(request: Request, name: str):
    db = request.app.state.db
    entries = [e for e in await _load_context_library(db) if e["name"] != name]
    await _save_context_library(db, entries)
    return entries


@router.get("/documents/{doc_id}/stream")
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
