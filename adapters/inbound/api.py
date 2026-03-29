from __future__ import annotations

import asyncio
import json as _json
import os as _os
from dataclasses import replace
from datetime import datetime, timezone
from typing import Optional

from fastapi import APIRouter, File, Form, Query, Request, UploadFile
from fastapi.responses import FileResponse, JSONResponse, StreamingResponse

from adapters.inbound.schemas import (
    ContextEntry, CreateContextBody, DocumentDetail, DocumentSummary,
    JobDetail, JobSummary, OkResponse,
    PaginatedContexts, PaginatedDocuments, PaginatedJobs, PaginatedPipelines,
    PatchDocumentBody, PatchJobBody, PatchRunBody, Pipeline, PipelineDetail, StageDetail, StageSummary,
    PutJobStatusBody, UpdateContextBody,
    CreateChatBody, PatchChatBody, SendMessageBody,
    decode_page_token, encode_page_token,
)

router = APIRouter(prefix="/api/v1")


# ── Helpers ────────────────────────────────────────────────────────────────────

def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


def _job_summary(job, title: Optional[str] = None) -> dict:
    return {
        "id": job.id,
        "document_id": job.document_id,
        "title": title,
        "stage": job.stage,
        "status": job.status,
        "created_at": job.created_at,
        "updated_at": job.updated_at,
    }


def _job_detail(job, title: Optional[str] = None) -> dict:
    return {
        **_job_summary(job, title),
        "options": job.options,
        "runs": job.runs,
    }


def _doc_summary(doc, current_job=None) -> dict:
    return {
        "id": str(doc.id),
        "title": doc.title,
        "current_job_id": current_job.id if current_job else None,
        "created_at": doc.created_at,
        "updated_at": doc.updated_at,
    }


async def _build_doc_detail(doc, db) -> dict:
    artifacts = await db.list_artifacts(doc.id)
    jobs = await db.list_jobs_for_document(doc.id)
    # current_job: prefer active (non-done/error) job, fall back to most recently updated
    active = next((j for j in reversed(jobs) if j.status in ("pending", "running", "waiting")), None)
    current_job = active or (jobs[-1] if jobs else None)
    return {
        "id": str(doc.id),
        "title": doc.title,
        "current_job_id": current_job.id if current_job else None,
        "additional_context": doc.additional_context,
        "linked_contexts": doc.linked_contexts,
        "artifacts": artifacts,
        "created_at": doc.created_at,
        "updated_at": doc.updated_at,
    }


async def _advance_pipeline(job, config, db, now: str) -> None:
    """After a job is set to done, upsert the next stage job to pending."""
    from core.domain.job import Job
    import uuid as _uuid
    stage_names = [s.name for s in config.stages]
    if job.stage not in stage_names:
        return
    idx = stage_names.index(job.stage)
    if idx + 1 >= len(stage_names):
        return
    next_stage = stage_names[idx + 1]
    existing = await db.get_job(job.document_id, next_stage)
    if existing is not None:
        await db.update_job_status(existing.id, "pending", now)
    else:
        from dataclasses import replace as _dc_replace
        next_job = Job(
            id=str(_uuid.uuid4()),
            document_id=job.document_id,
            stage=next_stage,
            status="pending",
            created_at=now,
            updated_at=now,
        )
        await db.upsert_job(next_job)


# ── Pipeline ───────────────────────────────────────────────────────────────────

@router.get("/pipelines", response_model=PaginatedPipelines, tags=["Pipelines"])
async def list_pipelines(request: Request):
    config = request.app.state.pipeline
    stages = [StageSummary(name=s.name, type=s.type, model=getattr(s, "model", None)) for s in config.stages]
    pipeline = Pipeline(id="pipeline", name="pipeline", stages=stages)
    return {"data": [pipeline], "next_page_token": None}


@router.get("/pipelines/{pipeline_id}", response_model=PipelineDetail, tags=["Pipelines"])
async def get_pipeline(request: Request, pipeline_id: str):
    if pipeline_id != "pipeline":
        return JSONResponse({"error": "not found"}, status_code=404)
    config = request.app.state.pipeline
    stages = []
    for s in config.stages:
        inputs = [s.input] if getattr(s, "input", None) else []
        if getattr(s, "inputs", None):
            inputs = list(s.inputs)
        raw_outputs = getattr(s, "outputs", None) or []
        outputs = [{"field": o.get("field", ""), "type": o.get("type", "text")} for o in raw_outputs]
        if not outputs and getattr(s, "output", None):
            outputs = [{"field": s.output, "type": "text"}]
        stages.append(StageDetail(
            name=s.name,
            type=s.type,
            model=getattr(s, "model", None),
            inputs=inputs or None,
            outputs=outputs or None,
            skip_if=getattr(s, "skip_if", None),
            start_if=getattr(s, "start_if", None),
            continue_if=getattr(s, "continue_if", None),
        ))
    return PipelineDetail(id="pipeline", name="pipeline", stages=stages)


# ── Documents ──────────────────────────────────────────────────────────────────

@router.get("/documents", response_model=PaginatedDocuments, tags=["Documents"])
async def list_documents(
    request: Request,
    sort: str = Query(default="pipeline"),
    page_size: int = Query(default=20, ge=1, le=200),
    page_token: Optional[str] = Query(default=None),
    stages: Optional[str] = Query(default=None),
    statuses: Optional[str] = Query(default=None),
):
    db = request.app.state.db
    token = decode_page_token(page_token) if page_token else None
    stage_list = [s.strip() for s in stages.split(",")] if stages else None
    status_list = [s.strip() for s in statuses.split(",")] if statuses else None
    docs, next_token = await db.list_documents_paginated(
        sort=sort, page_size=page_size, page_token=token,
        stages=stage_list, statuses=status_list,
    )

    # Batch fetch current_job_id for each document
    result = []
    for doc in docs:
        jobs = await db.list_jobs_for_document(doc.id)
        active = next((j for j in reversed(jobs) if j.status in ("pending", "running", "waiting")), None)
        current_job = active or (jobs[-1] if jobs else None)
        result.append(_doc_summary(doc, current_job))

    return {"data": result, "next_page_token": next_token}


@router.post("/documents", response_model=JobDetail, status_code=201, tags=["Documents"])
async def upload_document(
    request: Request,
    file: UploadFile = File(...),
    title: Optional[str] = Form(None),
    additional_context: Optional[str] = Form(None),
    linked_contexts: Optional[str] = Form(None),  # JSON array string
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

    linked_ctx_list = []
    if linked_contexts:
        try:
            linked_ctx_list = _json.loads(linked_contexts)
        except Exception:
            pass

    file_bytes = await file.read()
    doc = await ingest_upload(
        file_bytes=file_bytes,
        filename=filename,
        file_type=ext,
        title=title or None,
        additional_context=additional_context or "",
        linked_contexts=linked_ctx_list,
        db=db,
        vault_path=vault_path,
        filesystem=_fs,
    )
    if doc is None:
        return JSONResponse({"error": "Duplicate file — document already exists"}, status_code=409)

    # Create initial job row for first pipeline stage
    if config.stages:
        from core.domain.job import Job
        import uuid as _uuid
        first_stage = config.stages[0]
        now_str = _now()
        first_job = Job(
            id=str(_uuid.uuid4()),
            document_id=doc.id,
            stage=first_stage.name,
            status="pending",
            options={"require_context": bool(getattr(first_stage, "require_context", False))},
            created_at=now_str,
            updated_at=now_str,
        )
        await db.upsert_job(first_job)
        return _job_detail(first_job, doc.title)

    return JSONResponse({"error": "no pipeline stages configured"}, status_code=500)


@router.get("/documents/{doc_id}", response_model=DocumentDetail, tags=["Documents"])
async def get_document(request: Request, doc_id: str):
    db = request.app.state.db
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    return await _build_doc_detail(doc, db)


@router.patch("/documents/{doc_id}", response_model=DocumentDetail, tags=["Documents"])
async def patch_document(request: Request, doc_id: str, body: PatchDocumentBody):
    db = request.app.state.db
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)

    updated = doc
    now_str = _now()

    if body.title is not None:
        updated = replace(updated, title=body.title.strip() if body.title else None, updated_at=now_str)

    if body.additional_context is not None:
        updated = replace(updated, additional_context=body.additional_context.strip(), updated_at=now_str)

    if body.linked_contexts is not None:
        updated = replace(updated, linked_contexts=[str(c) for c in body.linked_contexts], updated_at=now_str)

    if updated is not doc:
        await db.update(updated)
        doc = await db.get(doc_id)

    return await _build_doc_detail(doc, db)


@router.delete("/documents/{doc_id}", response_model=OkResponse, tags=["Documents"])
async def delete_document(request: Request, doc_id: str):
    db = request.app.state.db
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    await db.delete(doc_id)
    return {"ok": True}


@router.get("/documents/{doc_id}/artifacts/{artifact_id}", tags=["Documents"])
async def get_artifact(request: Request, doc_id: str, artifact_id: str):
    db = request.app.state.db
    artifact = await db.get_artifact(doc_id, artifact_id)
    if artifact is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    vault_path = request.app.state.vault_path
    # Artifacts are stored at <vault_path>/artifacts/<artifact_id>/<filename>
    artifact_path = _os.path.join(vault_path, "artifacts", artifact_id, artifact["filename"])
    if not _os.path.exists(artifact_path):
        return JSONResponse({"error": "artifact file not found"}, status_code=404)
    return FileResponse(artifact_path, media_type=artifact["content_type"])


# ── Jobs ──────────────────────────────────────────────────────────────────────

@router.get("/jobs", response_model=PaginatedJobs, tags=["Jobs"])
async def list_jobs(
    request: Request,
    job_id: Optional[str] = Query(default=None),
    document_id: Optional[str] = Query(default=None),
    stages: Optional[str] = Query(default=None),
    statuses: Optional[str] = Query(default=None),
    sort: str = Query(default="pipeline"),
    page_size: int = Query(default=50, ge=1, le=1000),
    page_token: Optional[str] = Query(default=None),
):
    db = request.app.state.db
    job_id_list = [s.strip() for s in job_id.split(",")] if job_id else None
    doc_id_list = [s.strip() for s in document_id.split(",")] if document_id else None
    stage_list = [s.strip() for s in stages.split(",")] if stages else None
    status_list = [s.strip() for s in statuses.split(",")] if statuses else None
    token = decode_page_token(page_token) if page_token else None
    jobs, next_token = await db.list_jobs_paginated(
        job_id=job_id_list,
        document_id=doc_id_list,
        stages=stage_list,
        statuses=status_list,
        sort=sort,
        page_size=page_size,
        page_token=token,
    )
    # Batch fetch titles
    doc_cache: dict[str, Optional[str]] = {}
    for job in jobs:
        if job.document_id not in doc_cache:
            doc = await db.get(job.document_id)
            doc_cache[job.document_id] = doc.title if doc else None
    return {
        "data": [_job_summary(j, doc_cache.get(j.document_id)) for j in jobs],
        "next_page_token": next_token,
    }


@router.get("/jobs/{job_id}", response_model=JobDetail, tags=["Jobs"])
async def get_job(request: Request, job_id: str):
    db = request.app.state.db
    job = await db.get_job_by_id(job_id)
    if job is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    doc = await db.get(job.document_id)
    return _job_detail(job, doc.title if doc else None)


@router.patch("/jobs/{job_id}", response_model=JobDetail, tags=["Jobs"])
async def patch_job(request: Request, job_id: str, body: PatchJobBody):
    db = request.app.state.db
    job = await db.get_job_by_id(job_id)
    if job is None:
        return JSONResponse({"error": "not found"}, status_code=404)

    now_str = _now()
    if body.options is not None:
        new_options = dict(job.options)
        options_dict = body.options.model_dump(exclude_none=True)
        new_options.update(options_dict)
        await db.update_job_options(job_id, new_options, now_str)

    job = await db.get_job_by_id(job_id)
    doc = await db.get(job.document_id)
    return _job_detail(job, doc.title if doc else None)


@router.patch("/jobs/{job_id}/runs/{run_id}", response_model=None, tags=["Jobs"])
async def patch_run(request: Request, job_id: str, run_id: str, body: PatchRunBody):
    db = request.app.state.db
    job = await db.get_job_by_id(job_id)
    if job is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    now_str = _now()
    updated_run = await db.patch_run(job_id, run_id, body.model_dump(exclude_none=True), now_str)
    if updated_run is None:
        return JSONResponse({"error": "run not found"}, status_code=404)
    return updated_run


@router.put("/jobs/{job_id}/status", response_model=JobDetail, tags=["Jobs"])
async def put_job_status(request: Request, job_id: str, body: PutJobStatusBody):
    db = request.app.state.db
    config = request.app.state.pipeline
    job = await db.get_job_by_id(job_id)
    if job is None:
        return JSONResponse({"error": "not found"}, status_code=404)

    current = job.status
    target = body.status.value

    # Validate transition
    _valid_transitions: dict[str, set[str]] = {
        "running": {"error"},
        "waiting": {"pending", "done"},
        "error":   {"pending"},
        "done":    {"pending"},
    }
    if target not in _valid_transitions.get(current, set()):
        return JSONResponse(
            {"error": f"invalid transition: {current} → {target}"},
            status_code=422,
        )

    now_str = _now()
    await db.update_job_status(job_id, target, now_str)
    await db.append_event(job.document_id, job.stage, f"status_{target}", now_str)

    # Advance pipeline when approved (done)
    if target == "done":
        job = await db.get_job_by_id(job_id)
        await _advance_pipeline(job, config, db, now_str)

    # Cascade replay when rewinding a completed job
    if target == "pending" and current == "done":
        stage_names = [s.name for s in config.stages]
        await db.cascade_replay(job.document_id, job.stage, stage_names, now_str)

    job = await db.get_job_by_id(job_id)
    doc = await db.get(job.document_id)
    return _job_detail(job, doc.title if doc else None)


@router.get("/jobs/{job_id}/stream", tags=["Jobs"])
async def job_token_stream(request: Request, job_id: str):
    """SSE stream of LLM tokens while the job is running."""
    from adapters.outbound import streams as _streams
    db = request.app.state.db

    job = await db.get_job_by_id(job_id)
    if job is None:
        return JSONResponse({"error": "not found"}, status_code=404)

    initial_status = job.status

    async def generate():
        q = _streams.get_queue(job.document_id)
        last_check = asyncio.get_event_loop().time()
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
                if now - last_check > 3.0:
                    last_check = now
                    current_job = await db.get_job_by_id(job_id)
                    if current_job and current_job.status != initial_status:
                        yield f"event: done\ndata: {{}}\n\n"
                        break
                yield ": ping\n\n"

    return StreamingResponse(
        generate(),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


# ── Contexts ───────────────────────────────────────────────────────────────────

@router.get("/contexts", response_model=PaginatedContexts, tags=["Contexts"])
async def list_contexts(request: Request):
    db = request.app.state.db
    entries = await db.list_contexts()
    return {"data": entries, "next_page_token": None}


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


# ── Chat ───────────────────────────────────────────────────────────────────────

_DEFAULT_RAG = {"enabled": True, "max_sources": 5, "minimum_score": 0.0}


@router.get("/chats", tags=["Chat"])
async def list_chats(
    request: Request,
    page_size: int = Query(default=20, ge=1, le=100),
    before_id: Optional[str] = Query(default=None),
):
    db = request.app.state.db
    chats = await db.list_chats(page_size=page_size, before_id=before_id)
    next_before_id = chats[-1]["id"] if len(chats) == page_size else None
    return {"data": chats, "next_before_id": next_before_id}


@router.post("/chats", tags=["Chat"])
async def create_chat(request: Request, body: CreateChatBody):
    db = request.app.state.db
    rag = body.rag_retrieval.model_dump() if body.rag_retrieval else _DEFAULT_RAG
    chat = await db.create_chat(
        system_prompt=body.system_prompt or "",
        rag_retrieval=rag,
    )
    return chat


@router.get("/chats/{chat_id}", tags=["Chat"])
async def get_chat(request: Request, chat_id: str):
    db = request.app.state.db
    chat = await db.get_chat(chat_id)
    if chat is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    messages = await db.list_chat_messages(chat_id)
    return {**chat, "messages": messages}


@router.patch("/chats/{chat_id}", tags=["Chat"])
async def patch_chat(request: Request, chat_id: str, body: PatchChatBody):
    db = request.app.state.db
    chat = await db.get_chat(chat_id)
    if chat is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    kwargs: dict = {}
    if body.title is not None:
        kwargs["title"] = body.title
    if body.system_prompt is not None:
        kwargs["system_prompt"] = body.system_prompt
    if body.rag_retrieval is not None:
        kwargs["rag_retrieval"] = body.rag_retrieval.model_dump()
    updated = await db.update_chat(chat_id, **kwargs)
    return updated


@router.delete("/chats/{chat_id}", tags=["Chat"])
async def delete_chat(request: Request, chat_id: str):
    db = request.app.state.db
    deleted = await db.delete_chat(chat_id)
    if not deleted:
        return JSONResponse({"error": "not found"}, status_code=404)
    return JSONResponse(None, status_code=204)


@router.post("/chats/{chat_id}/messages", tags=["Chat"])
async def send_chat_message(request: Request, chat_id: str, body: SendMessageBody):
    from adapters.outbound import ollama as _ollama
    from adapters.outbound import qdrant as _qdrant

    db = request.app.state.db
    chat = await db.get_chat(chat_id)
    if chat is None:
        return JSONResponse({"error": "not found"}, status_code=404)

    rag = chat.get("rag_retrieval") or _DEFAULT_RAG
    system_prompt = chat.get("system_prompt") or ""
    content = body.content.strip()

    history = await db.list_chat_messages(chat_id)
    await db.append_chat_message(chat_id, "user", content)

    if not chat["title"]:
        await db.update_chat(chat_id, title=content[:60].strip())

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
        buffer: list[str] = []
        final_sources: list[dict] = []
        try:
            try:
                query_vector = await _ollama.generate_embed(ollama_base_url, embed_model, content)
            except Exception as exc:
                yield f"event: error\ndata: {_json.dumps({'error': str(exc)})}\n\n"
                return

            sources: list[dict] = []
            top_k = rag.get("max_sources", 5) if rag.get("enabled", True) else 0
            if qdrant_url and top_k > 0:
                try:
                    sources = await _qdrant.search(qdrant_url, qdrant_collection, query_vector, top_k, qdrant_api_key)
                except Exception:
                    pass

            source_summaries = [
                {
                    "document_id": s.get("doc_id", ""),
                    "title": s.get("title") or "Untitled",
                    "summary": s.get("summary", ""),
                    "date_month": s.get("date_month", ""),
                    "score": round(s.get("score", 0.0), 3),
                }
                for s in sources
            ]
            final_sources = source_summaries
            yield f"event: sources\ndata: {_json.dumps(source_summaries)}\n\n"

            notes_block = ""
            for s in sources:
                title = s.get("title") or "Untitled"
                date = s.get("date_month", "")
                text = s.get("text", s.get("summary", ""))
                header = f"Title: {title}" + (f" ({date})" if date else "")
                notes_block += f"---\n{header}\n{text}\n\n"

            ctx_block = f"\nAdditional context:\n{system_prompt}\n" if system_prompt else ""
            notes_section = f"\nRetrieved notes:\n{notes_block}" if notes_block else "\n(No matching notes found.)\n"
            system_content = (
                "You are a helpful assistant with access to a personal notes knowledge base. "
                "Answer based on the retrieved notes. If they don't contain enough information, say so."
                f"{ctx_block}"
                f"{notes_section}"
            )

            chat_messages = [{"role": "system", "content": system_content}] + [
                {"role": m["role"], "content": m["content"]}
                for m in history
                if m.get("role") in ("user", "assistant") and m.get("content")
            ] + [{"role": "user", "content": content}]

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
                    buffer.append(item)
                    yield f"event: token\ndata: {_json.dumps({'text': item})}\n\n"
            finally:
                state["stopped"] = True
                llm_task.cancel()

            yield f"event: done\ndata: {{}}\n\n"
        finally:
            if buffer:
                await db.append_chat_message(
                    chat_id, "assistant", "".join(buffer),
                    sources=final_sources if final_sources else None,
                )

    return StreamingResponse(
        generate(),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )
