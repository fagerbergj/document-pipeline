from __future__ import annotations

import asyncio
import json as _json
import os as _os
from dataclasses import replace
from datetime import datetime, timezone
from typing import Optional

from fastapi import APIRouter, Request, Query, UploadFile, File
from fastapi.responses import FileResponse, JSONResponse, StreamingResponse

from core.services import review as review_service
from adapters.inbound.schemas import (
    ApproveBody, ClarifyBody, ContextEntry, Counts, DocumentDetail,
    DocumentSummary, OkResponse, SaveContextBody, SaveContextEntryBody,
    StagesResponse, UpdateTitleBody,
)

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
            "context_updates": _clean_context_updates(sdata.get("context_updates", "")),
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


_CTX_NULL_VALS = {"none", "null", "n/a", "nothing", "no updates", "no new information"}

def _clean_context_updates(val: str) -> str:
    v = (val or "").strip()
    return "" if v.lower() in _CTX_NULL_VALS else v


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


@router.get("/pipeline/stages", response_model=StagesResponse, tags=["pipeline"])
async def get_stages(request: Request):
    config = request.app.state.pipeline
    return {"stages": [s.name for s in config.stages]}


@router.get("/counts", response_model=Counts, tags=["pipeline"])
async def get_counts(request: Request):
    db = request.app.state.db
    counts = await db.status_counts()
    return counts


@router.get("/documents", response_model=list[DocumentSummary], tags=["documents"])
async def list_documents(
    request: Request,
    stages: Optional[str] = Query(default=None),
    states: Optional[str] = Query(default=None),
    sort: str = Query(default="pipeline"),
):
    db = request.app.state.db
    config = request.app.state.pipeline
    stage_list = [s.strip() for s in stages.split(",")] if stages else None
    state_list = [s.strip() for s in states.split(",")] if states else None
    docs = await db.list_documents(stages=stage_list, states=state_list, sort=sort)
    return [_doc_summary(doc, config) for doc in docs]


@router.get("/documents/{doc_id}", response_model=DocumentDetail, tags=["documents"])
async def get_document(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    events = await db.get_events(doc_id)
    return _build_doc_detail(doc, config, events)


@router.get("/documents/{doc_id}/image", tags=["documents"])
async def get_document_image(request: Request, doc_id: str):
    db = request.app.state.db
    doc = await db.get(doc_id)
    if doc is None or not doc.png_path or not _os.path.exists(doc.png_path):
        return JSONResponse({"error": "not found"}, status_code=404)
    return FileResponse(doc.png_path, media_type="image/png")


@router.delete("/documents/{doc_id}", response_model=OkResponse, tags=["documents"])
async def delete_document(request: Request, doc_id: str):
    db = request.app.state.db
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    await db.delete(doc_id)
    return {"ok": True}


@router.post("/documents/{doc_id}/title", response_model=DocumentDetail, tags=["documents"])
async def update_title(request: Request, doc_id: str, body: UpdateTitleBody):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    new_title = body.title.strip()
    if new_title:
        await db.update(replace(doc, title=new_title, updated_at=_now()))
    doc = await db.get(doc_id)
    return _build_doc_detail(doc, config)


@router.post("/documents/{doc_id}/context", response_model=DocumentDetail, tags=["documents"])
async def save_context(request: Request, doc_id: str, body: SaveContextBody):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    document_context = body.document_context.strip()
    stage_data = dict(doc.stage_data)
    ingest = dict(stage_data.get("_ingest", {}))
    ingest["document_context"] = document_context
    stage_data["_ingest"] = ingest
    await db.update(replace(doc, stage_data=stage_data, updated_at=_now()))
    doc = await db.get(doc_id)
    return _build_doc_detail(doc, config)


@router.post("/documents/{doc_id}/set-context", response_model=DocumentDetail, tags=["documents"])
async def set_context_and_run(request: Request, doc_id: str, body: SaveContextBody):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    document_context = body.document_context.strip()
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


@router.post("/documents/{doc_id}/approve", response_model=DocumentDetail, tags=["documents"])
async def approve_document(request: Request, doc_id: str, body: ApproveBody):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    edited_text = body.edited_text.strip()
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


@router.post("/documents/{doc_id}/reject", response_model=DocumentDetail, tags=["documents"])
async def reject_document(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    updated = await review_service.reject(doc, config, db, _now())
    return _build_doc_detail(updated, config)


@router.post("/documents/{doc_id}/clarify", response_model=DocumentDetail, tags=["documents"])
async def clarify_document(request: Request, doc_id: str, body: ClarifyBody):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
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
        doc, clarification_responses, config, db, _now(), free_prompt=free_prompt, edited_text=edited_text
    )
    return _build_doc_detail(updated, config)


@router.delete("/documents/{doc_id}/errors", response_model=DocumentDetail, tags=["documents"])
async def clear_errors(request: Request, doc_id: str):
    db = request.app.state.db
    config = request.app.state.pipeline
    doc = await db.get(doc_id)
    if doc is None:
        return JSONResponse({"error": "not found"}, status_code=404)
    await db.clear_errors(doc_id)
    events = await db.get_events(doc_id)
    return _build_doc_detail(doc, config, events)


@router.post("/documents/{doc_id}/stop", response_model=DocumentDetail, tags=["documents"])
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


@router.post("/documents/{doc_id}/retry", response_model=DocumentDetail, tags=["documents"])
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


@router.post("/documents/{doc_id}/replay/{stage_name}", response_model=DocumentDetail, tags=["documents"])
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


@router.get("/context-library", response_model=list[ContextEntry], tags=["context"])
async def get_context_library(request: Request):
    db = request.app.state.db
    entries = await _load_context_library(db)
    return entries


@router.post("/context-library", response_model=list[ContextEntry], tags=["context"])
async def save_context_entry(request: Request, body: SaveContextEntryBody):
    db = request.app.state.db
    name = body.name.strip()
    text = body.text.strip()
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


@router.delete("/context-library/{name}", response_model=list[ContextEntry], tags=["context"])
async def delete_context_entry(request: Request, name: str):
    db = request.app.state.db
    entries = [e for e in await _load_context_library(db) if e["name"] != name]
    await _save_context_library(db, entries)
    return entries


@router.post("/query", tags=["query"])
async def query_knowledge_base(request: Request):
    """RAG chat: embed latest user message → search Qdrant → stream LLM reply as SSE."""
    from adapters.outbound import ollama as _ollama
    from adapters.outbound import qdrant as _qdrant

    body = await request.json()
    # messages: [{role: "user"|"assistant", content: str}]
    messages: list[dict] = body.get("messages") or []
    context: str = (body.get("context") or "").strip()
    top_k: int = int(body.get("top_k") or 5)

    # Latest user message is the one to embed + search
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
        # 1. Embed the latest user message
        try:
            query_vector = await _ollama.generate_embed(ollama_base_url, embed_model, latest_query)
        except Exception as exc:
            yield f"event: error\ndata: {_json.dumps({'error': str(exc)})}\n\n"
            return

        # 2. Search Qdrant
        sources: list[dict] = []
        if qdrant_url:
            try:
                sources = await _qdrant.search(qdrant_url, qdrant_collection, query_vector, top_k, qdrant_api_key)
            except Exception as exc:
                logger.warning("Qdrant search failed: %s", exc)

        # 3. Emit sources
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

        # 4. Build system message with RAG context (refreshed each turn)
        notes_block = ""
        for s in sources:
            title = s.get("title") or "Untitled"
            date = s.get("date_month", "")
            text = s.get("text", s.get("summary", ""))
            header = f"Title: {title}" + (f" ({date})" if date else "")
            notes_block += f"---\n{header}\n{text}\n\n"

        ctx_block = f"\nAdditional context:\n{context}\n" if context else ""
        notes_section = f"\nRetrieved notes (most relevant to the latest question):\n{notes_block}" if notes_block else "\n(No matching notes found.)\n"
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

        # 5. Stream chat response
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
                    is_stopped=is_stopped_fn,
                    on_chunk=on_chunk,
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


@router.get("/documents/{doc_id}/stream", tags=["documents"])
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
