from __future__ import annotations

import asyncio
import json as _json
import os as _os
from dataclasses import replace
from datetime import datetime, timezone
from typing import Optional

from fastapi import APIRouter, File, Form, Query, Request, UploadFile
from fastapi.responses import FileResponse, JSONResponse, StreamingResponse

from core.services import review as review_service
from adapters.inbound.schemas import (
    ApproveEvent, ClarifyEvent, ClearErrorsEvent, ContextEntry,
    CreateContextBody, DocumentDetail, DocumentSummary,
    JobDetail, JobEventBody, JobEventRecord, JobSummary,
    OkResponse, PaginatedContexts, PaginatedDocuments,
    PaginatedJobEvents, PaginatedJobs, PaginatedPipelines,
    PatchDocumentBody, PipelineDetail, PipelineStageConfig, PipelineSummary,
    ProvideContextEvent, ReplayEvent, RejectEvent, RetryEvent, StopEvent,
    ChatRequest, UpdateContextBody,
    decode_page_token, encode_page_token,
)

router = APIRouter(prefix="/api/v1")


# ── Helpers ────────────────────────────────────────────────────────────────────

def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


def _clean_context_updates(val: str) -> str:
    _NULL = {"none", "null", "n/a", "nothing", "no updates", "no new information"}
    v = (val or "").strip()
    return "" if v.lower() in _NULL else v


def _build_doc_detail(doc, config) -> dict:
    """Pure document data: title, context, artifacts."""
    document_context = doc.stage_data.get("_ingest", {}).get("document_context", "")
    context_ref = doc.context_ref

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

    return {
        "id": doc.id,
        "title": doc.title,
        "document_context": document_context,
        "context_ref": context_ref,
        "has_image": bool(doc.png_path and _os.path.exists(doc.png_path)),
        "stage_displays": stage_displays,
        "created_at": doc.created_at,
        "updated_at": doc.updated_at,
    }


def _build_job_detail(doc, config) -> dict:
    """Job execution state: stage, state, review, replay_stages."""
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
    has_context = bool(document_context or doc.context_ref)
    context_required = (
        doc.stage_state in ("waiting", "pending")
        and stage_def is not None
        and not has_llm_output
        and (stage_def.require_context or start_if.get("context_provided"))
        and not has_context
    )
    needs_context = (
        doc.stage_state in ("waiting", "pending")
        and stage_def is not None
        and (start_if.get("context_provided") or stage_def.require_context)
        and not has_context
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

        review = {
            "stage_name": doc.current_stage,
            "input_field": stage_def.input,
            "output_field": stage_def.output if stage_def.output else None,
            "input_text": input_text,
            "output_text": output_text,
            "is_single_output": bool(stage_def.output),
            "confidence": sdata.get("confidence", ""),
            "qa_rounds": len(sdata.get("qa_history", [])),
            "clarification_requests": sdata.get("clarification_requests", []),
            "document_context_update": _clean_context_updates(sdata.get("document_context_update", "")),
            "linked_context_update": _clean_context_updates(sdata.get("linked_context_update", "")),
            "context_ref": doc.context_ref,
        }

    stage_names = [s.name for s in config.stages]
    current_idx = stage_names.index(doc.current_stage) if doc.current_stage in stage_names else -1
    replay_stages = [
        {"name": s.name}
        for s in config.stages
        if s.name in doc.stage_data and (current_idx == -1 or stage_names.index(s.name) < current_idx)
    ]

    return {
        "doc_id": doc.id,
        "current_stage": doc.current_stage,
        "stage_state": doc.stage_state,
        "needs_context": needs_context,
        "context_required": context_required,
        "review": review,
        "replay_stages": replay_stages,
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
        "needs_context": needs_context,
        "created_at": doc.created_at,
        "updated_at": doc.updated_at,
    }


def _job_summary(doc, config) -> dict:
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
        "doc_id": doc.id,
        "title": doc.title,
        "current_stage": doc.current_stage,
        "stage_state": doc.stage_state,
        "needs_context": needs_context,
        "created_at": doc.created_at,
        "updated_at": doc.updated_at,
    }


# ── Pipeline ───────────────────────────────────────────────────────────────────

@router.get("/pipelines", response_model=PaginatedPipelines, tags=["Pipelines"])
async def list_pipelines(request: Request):
    config = request.app.state.pipeline
    summary = PipelineSummary(id="pipeline", name="pipeline", stage_count=len(config.stages))
    return {"data": [summary], "nextPageToken": None}


@router.get("/pipelines/{pipeline_id}", response_model=PipelineDetail, tags=["Pipelines"])
async def get_pipeline(request: Request, pipeline_id: str):
    if pipeline_id != "pipeline":
        return JSONResponse({"error": "not found"}, status_code=404)
    config = request.app.state.pipeline
    stages = [
        PipelineStageConfig(name=s.name, type=s.type, model=getattr(s, "model", None))
        for s in config.stages
    ]
    return PipelineDetail(id="pipeline", name="pipeline", stages=stages)


# ── Documents ──────────────────────────────────────────────────────────────────

@router.get("/documents", response_model=PaginatedDocuments, tags=["Documents"])
async def list_documents(
    request: Request,
    stages: Optional[str] = Query(default=None),
    states: Optional[str] = Query(default=None),
    sort: str = Query(default="pipeline"),
    pageSize: int = Query(default=50, ge=1, le=200),
    pageToken: Optional[str] = Query(default=None),
):
    db = request.app.state.db
    config = request.app.state.pipeline
    stage_list = [s.strip() for s in stages.split(",")] if stages else None
    state_list = [s.strip() for s in states.split(",")] if states else None
    token = decode_page_token(pageToken) if pageToken else None
    docs, next_token = await db.list_documents_paginated(
        stages=stage_list, states=state_list, sort=sort,
        page_size=pageSize, page_token=token,
    )
    return {"data": [_doc_summary(d, config) for d in docs], "nextPageToken": next_token}


@router.post("/documents", response_model=JobDetail, status_code=201, tags=["Documents"])
async def upload_document(
    request: Request,
    file: UploadFile = File(...),
    title: Optional[str] = Form(None),
    document_context: Optional[str] = Form(None),
    context_ref: Optional[str] = Form(None),
):
    from core.services.ingest import ingest_upload, SUPPORTED_TYPES
    from adapters.outbound import filesystem as _fs
    db = request.app.state.db
    config = request.app.state.pipeline
    vault_path = request.app.state.vault_path

    filename = file.filename or "upload"
    ext = filename.rsplit(".", 1)[-1].lower() if "." in filename else ""
    if ext not in SUPPORTED_TYPES:
        return JSONResponse(
            {"error": f"Unsupported file type '.{ext}'. Supported: {', '.join(sorted(SUPPORTED_TYPES))}"},
            status_code=400,
        )

    file_bytes = await file.read()
    doc = await ingest_upload(
        file_bytes=file_bytes,
        filename=filename,
        file_type=ext,
        title=title or None,
        document_context=document_context or "",
        context_ref=context_ref or None,
        db=db,
        vault_path=vault_path,
        filesystem=_fs,
    )
    if doc is None:
        return JSONResponse({"error": "Duplicate file — document already exists"}, status_code=409)

    return _build_job_detail(doc, config)


@router.get("/documents/{doc_id}", response_model=DocumentDetail, tags=["Documents"])
async def get_document(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    return _build_doc_detail(doc, config)


@router.patch("/documents/{doc_id}", response_model=DocumentDetail, tags=["Documents"])
async def patch_document(request: Request, doc_id: str, body: PatchDocumentBody):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)

    updated = doc
    now_str = _now()

    if body.title is not None and body.title.strip():
        updated = replace(updated, title=body.title.strip(), updated_at=now_str)

    if body.document_context is not None:
        stage_data = dict(updated.stage_data)
        ingest = dict(stage_data.get("_ingest", {}))
        ingest["document_context"] = body.document_context.strip()
        stage_data["_ingest"] = ingest
        updated = replace(updated, stage_data=stage_data, updated_at=now_str)

    if body.context_ref is not None:
        updated = replace(updated, context_ref=body.context_ref or None, updated_at=now_str)

    if updated is not doc:
        await db.update(updated)
        doc = await db.get(doc_id)

    return _build_doc_detail(doc, config)


@router.delete("/documents/{doc_id}", response_model=OkResponse, tags=["Documents"])
async def delete_document(request: Request, doc_id: str):
    db = request.app.state.db
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    await db.delete(doc_id)
    return {"ok": True}


@router.get("/documents/{doc_id}/image", tags=["Documents"])
async def get_document_image(request: Request, doc_id: str):
    db = request.app.state.db
    doc = await db.get(doc_id)
    if doc is None or not doc.png_path or not _os.path.exists(doc.png_path):
        return JSONResponse({"error": "not found"}, status_code=404)
    return FileResponse(doc.png_path, media_type="image/png")


# ── Top-level Jobs list ────────────────────────────────────────────────────────

@router.get("/jobs", response_model=PaginatedJobs, tags=["Jobs"])
async def list_jobs(
    request: Request,
    stages: Optional[str] = Query(default=None),
    states: Optional[str] = Query(default=None),
    sort: str = Query(default="pipeline"),
    pageSize: int = Query(default=50, ge=1, le=1000),
    pageToken: Optional[str] = Query(default=None),
):
    db = request.app.state.db
    config = request.app.state.pipeline
    stage_list = [s.strip() for s in stages.split(",")] if stages else None
    state_list = [s.strip() for s in states.split(",")] if states else None
    token = decode_page_token(pageToken) if pageToken else None
    docs, next_token = await db.list_documents_paginated(
        stages=stage_list, states=state_list, sort=sort,
        page_size=pageSize, page_token=token,
    )
    return {"data": [_job_summary(d, config) for d in docs], "nextPageToken": next_token}


# ── Per-document Jobs ──────────────────────────────────────────────────────────

@router.get("/documents/{doc_id}/jobs", response_model=JobDetail, tags=["Jobs"])
async def get_job(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    return _build_job_detail(doc, config)


@router.get("/documents/{doc_id}/jobs/stream", tags=["Jobs"])
async def job_token_stream(request: Request, doc_id: str):
    """SSE stream of LLM tokens while the document's job is running."""
    from adapters.outbound import streams as _streams
    db = request.app.state.db

    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    initial_state = f"{doc.current_stage}:{doc.stage_state}"

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


@router.get("/documents/{doc_id}/jobs/events", response_model=PaginatedJobEvents, tags=["Jobs"])
async def list_job_events(
    request: Request,
    doc_id: str,
    pageSize: int = Query(default=100, ge=1, le=500),
    pageToken: Optional[str] = Query(default=None),
):
    db = request.app.state.db
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)

    after_id: Optional[int] = None
    if pageToken:
        token = decode_page_token(pageToken)
        if token:
            after_id = token.get("id")

    events, next_after = await db.get_events_paginated(doc_id, page_size=pageSize, after_id=after_id)
    next_token = encode_page_token(None, str(next_after)) if next_after is not None else None
    return {"data": events, "nextPageToken": next_token}


@router.post("/documents/{doc_id}/jobs/events", response_model=JobDetail, tags=["Jobs"])
async def post_job_event(request: Request, doc_id: str, body: JobEventBody):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)

    now_str = _now()
    event_type = body.type

    # ── approve ────────────────────────────────────────────────────────────────
    if event_type == "approve":
        edited_text = body.edited_text.strip()
        stage_def = config.get_stage(doc.current_stage)
        if edited_text and stage_def and stage_def.output:
            stage_data = dict(doc.stage_data)
            entry = dict(stage_data.get(stage_def.name, {}))
            entry[stage_def.output] = edited_text
            stage_data[stage_def.name] = entry
            doc = replace(doc, stage_data=stage_data)
            await db.update(doc)
        updated = await review_service.approve(doc, config, db, now_str)
        return _build_job_detail(updated, config)

    # ── reject ─────────────────────────────────────────────────────────────────
    if event_type == "reject":
        updated = await review_service.reject(doc, config, db, now_str)
        return _build_job_detail(updated, config)

    # ── clarify ────────────────────────────────────────────────────────────────
    if event_type == "clarify":
        answers = body.answers
        free_prompt = body.free_prompt.strip()
        edited_text = body.edited_text.strip()
        stage_name = doc.current_stage
        existing_requests = (doc.stage_data.get(stage_name) or {}).get("clarification_requests", [])
        clarification_responses = [
            {"segment": req["segment"], "answer": answers.get(str(i), "")}
            for i, req in enumerate(existing_requests)
        ]
        updated = await review_service.reject_with_clarifications(
            doc, clarification_responses, config, db, now_str,
            free_prompt=free_prompt, edited_text=edited_text,
        )
        return _build_job_detail(updated, config)

    # ── retry ──────────────────────────────────────────────────────────────────
    if event_type == "retry":
        updated = replace(doc, stage_state="pending", updated_at=now_str)
        await db.update(updated)
        await db.append_event(doc_id, doc.current_stage, "retried", now_str)
        return _build_job_detail(updated, config)

    # ── stop ───────────────────────────────────────────────────────────────────
    if event_type == "stop":
        updated = replace(doc, stage_state="error", updated_at=now_str)
        await db.update(updated)
        await db.append_event(doc_id, doc.current_stage, "stopped", now_str)
        return _build_job_detail(updated, config)

    # ── replay ─────────────────────────────────────────────────────────────────
    if event_type == "replay":
        stage_name = body.stage
        stage_names = [s.name for s in config.stages]
        if stage_name not in stage_names:
            return JSONResponse({"error": "unknown stage"}, status_code=400)
        replay_idx = stage_names.index(stage_name)
        stage_data = {
            k: v for k, v in doc.stage_data.items()
            if k == "_ingest" or k not in stage_names[replay_idx:]
        }
        updated = replace(doc, current_stage=stage_name, stage_state="pending",
                          stage_data=stage_data, updated_at=now_str)
        await db.update(updated)
        await db.append_event(doc_id, stage_name, "replayed", now_str)
        return _build_job_detail(updated, config)

    # ── provide_context ────────────────────────────────────────────────────────
    if event_type == "provide_context":
        stage_data = dict(doc.stage_data)
        ingest = dict(stage_data.get("_ingest", {}))
        if body.document_context is not None:
            ingest["document_context"] = body.document_context.strip()
        stage_data["_ingest"] = ingest
        new_context_ref = (body.context_ref or None) if body.context_ref is not None else doc.context_ref

        stage_def = config.get_stage(doc.current_stage)
        sdata = stage_data.get(doc.current_stage, {})
        has_output = bool(sdata and stage_def and (
            (stage_def.output and sdata.get(stage_def.output)) or
            (stage_def.outputs and any(sdata.get(o.get("field", "")) for o in (stage_def.outputs or [])))
        ))
        new_state = doc.stage_state
        if doc.stage_state == "waiting" and not has_output:
            new_state = "pending"
        updated = replace(doc, stage_data=stage_data, context_ref=new_context_ref,
                          stage_state=new_state, updated_at=now_str)
        await db.update(updated)
        await db.append_event(doc_id, doc.current_stage, "context_set", now_str)
        return _build_job_detail(updated, config)

    # ── clear_errors ───────────────────────────────────────────────────────────
    if event_type == "clear_errors":
        await db.clear_errors(doc_id)
        doc = await db.get(doc_id)
        return _build_job_detail(doc, config)

    return JSONResponse({"error": f"unknown event type: {event_type}"}, status_code=400)


# ── Contexts ───────────────────────────────────────────────────────────────────

@router.get("/contexts", response_model=PaginatedContexts, tags=["Contexts"])
async def list_contexts(request: Request):
    db = request.app.state.db
    entries = await db.list_contexts()
    return {"data": entries, "nextPageToken": None}


@router.post("/contexts", response_model=ContextEntry, tags=["Contexts"])
async def create_context(request: Request, body: CreateContextBody):
    db = request.app.state.db
    entry = await db.create_context_entry(body.name.strip(), body.text.strip())
    return entry


@router.patch("/contexts/{context_id}", response_model=ContextEntry, tags=["Contexts"])
async def update_context(request: Request, context_id: str, body: UpdateContextBody):
    db = request.app.state.db
    entry = await db.update_context_entry(
        context_id,
        name=body.name.strip() if body.name is not None else None,
        text=body.text.strip() if body.text is not None else None,
    )
    if entry is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    return entry


@router.delete("/contexts/{context_id}", response_model=OkResponse, tags=["Contexts"])
async def delete_context(request: Request, context_id: str):
    db = request.app.state.db
    deleted = await db.delete_context_entry(context_id)
    if not deleted:
        return JSONResponse({"error": "not found"}, status_code=404)
    return {"ok": True}


# ── Chat (RAG) ─────────────────────────────────────────────────────────────────

@router.post("/chats", tags=["Chat"])
async def create_chat(request: Request, body: ChatRequest):
    """RAG chat: embed latest user message → search Qdrant → stream LLM reply as SSE."""
    from adapters.outbound import ollama as _ollama
    from adapters.outbound import qdrant as _qdrant

    messages = [m.model_dump(mode='json') for m in body.messages]
    context = body.context.strip()
    top_k = body.top_k

    user_messages = [m for m in messages if m.get("role") == "user"]
    if not user_messages:
        return JSONResponse({"error": "no user message provided"}, status_code=400)
    latest_query = user_messages[-1].get("content", "").strip()

    ollama_base_url: str = request.app.state.ollama_base_url
    config = request.app.state.pipeline

    embed_stage = next((s for s in config.stages if s.type == "embed"), None)
    embed_model: str = (embed_stage.model if embed_stage else None) or _os.environ.get("EMBED_MODEL", "nomic-embed-text:v1.5")
    query_model: str = _os.environ.get("QUERY_MODEL") or _os.environ.get("CLARIFY_MODEL", "qwen3:4b")

    qdrant_url: str = ""
    qdrant_collection: str = "remarkable"
    qdrant_api_key: str | None = None
    if embed_stage:
        for dest in (embed_stage.destinations or []):
            if dest.get("type") == "qdrant":
                qdrant_url = dest.get("url", "")
                qdrant_collection = dest.get("collection", "remarkable")
                qdrant_api_key = dest.get("api_key") or None
                break

    async def generate():
        try:
            query_vector = await _ollama.generate_embed(ollama_base_url, embed_model, latest_query)
        except Exception as exc:
            yield f"event: error\ndata: {_json.dumps({'error': str(exc)})}\n\n"
            return

        sources: list[dict] = []
        if qdrant_url:
            try:
                sources = await _qdrant.search(qdrant_url, qdrant_collection, query_vector, top_k, qdrant_api_key)
            except Exception:
                pass

        source_summaries = [
            {
                "doc_id": s.get("doc_id", ""),
                "title": s.get("title") or "Untitled",
                "summary": s.get("summary", ""),
                "date_month": s.get("date_month", ""),
                "score": round(s.get("score", 0.0), 3),
            }
            for s in sources
        ]
        yield f"event: sources\ndata: {_json.dumps(source_summaries)}\n\n"

        notes_block = ""
        for s in sources:
            title = s.get("title") or "Untitled"
            date = s.get("date_month", "")
            text = s.get("text", s.get("summary", ""))
            header = f"Title: {title}" + (f" ({date})" if date else "")
            notes_block += f"---\n{header}\n{text}\n\n"

        ctx_block = f"\nAdditional context:\n{context}\n" if context else ""
        notes_section = f"\nRetrieved notes:\n{notes_block}" if notes_block else "\n(No matching notes found.)\n"
        system_content = (
            "You are a helpful assistant with access to a personal notes knowledge base. "
            "Answer based on the retrieved notes. If they don't contain enough information, say so."
            f"{ctx_block}"
            f"{notes_section}"
        )

        chat_messages = [{"role": "system", "content": system_content}] + [
            {"role": m["role"], "content": m["content"]}
            for m in messages
            if m.get("role") in ("user", "assistant") and m.get("content")
        ]

        state = {"stopped": False}
        token_queue: asyncio.Queue = asyncio.Queue()

        async def on_chunk(token: str):
            await token_queue.put(token)

        async def is_stopped_fn():
            return state["stopped"]

        async def run_llm():
            try:
                await _ollama.chat_stream(
                    ollama_base_url, query_model, chat_messages,
                    is_stopped=is_stopped_fn, on_chunk=on_chunk,
                )
            except Exception as exc:
                await token_queue.put(("__error__", str(exc)))
            finally:
                await token_queue.put(None)

        llm_task = asyncio.create_task(run_llm())
        try:
            while True:
                if await request.is_disconnected():
                    state["stopped"] = True
                    break
                try:
                    item = await asyncio.wait_for(token_queue.get(), timeout=30.0)
                except asyncio.TimeoutError:
                    yield ": ping\n\n"
                    continue
                if item is None:
                    break
                if isinstance(item, tuple) and item[0] == "__error__":
                    yield f"event: error\ndata: {_json.dumps({'error': item[1]})}\n\n"
                    break
                yield f"event: token\ndata: {_json.dumps({'text': item})}\n\n"
        finally:
            state["stopped"] = True
            llm_task.cancel()

        yield f"event: done\ndata: {{}}\n\n"

    return StreamingResponse(
        generate(),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )
