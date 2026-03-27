from __future__ import annotations

import asyncio
import logging
import traceback
from dataclasses import replace
from datetime import datetime, timezone
from pathlib import Path
from typing import Optional

from core.domain.document import (
    Document,
    advance,
    set_done,
    set_error,
    set_running,
    set_waiting,
)
from core.domain.pipeline import PipelineConfig, StageDefinition

logger = logging.getLogger(__name__)

# Stage types implemented in this phase. Worker silently skips others.
_HANDLED_TYPES = {"computer_vision", "llm_text", "embed"}

_GENERIC_FILENAMES = {"remarkable", "untitled", "image", "attachment", "document"}

_CONFIDENCE_LEVELS = {"low": 0, "medium": 1, "high": 2}


def _check_start_if(doc: Document, stage: StageDefinition) -> bool:
    """Return False if start conditions are not yet met (should park as waiting)."""
    rules = stage.start_if or {}
    # backward compat: require_context maps to start_if.context_provided
    if stage.require_context or rules.get("context_provided"):
        if not doc.stage_data.get("_ingest", {}).get("document_context"):
            return False
    return True


def _check_continue_if(stage_data: dict, stage: StageDefinition) -> bool:
    """Return True if the LLM output satisfies a continue rule (auto-advance without review)."""
    rules = stage.continue_if
    if not rules:
        return True  # no rules → always auto-advance
    sdata = stage_data.get(stage.name, {})
    for rule in rules:
        if "confidence" in rule:
            required = _CONFIDENCE_LEVELS.get(rule["confidence"], 2)
            actual = _CONFIDENCE_LEVELS.get(sdata.get("confidence", "low"), 0)
            if actual >= required:
                return True
        # user_approves: true means this rule can never be auto-satisfied
    return False


def _sanitize(name: str) -> str:
    safe = "".join(c if c.isalnum() or c in "_-" else "_" for c in name)
    return safe.strip("_") or "untitled"


def _extract_title(ingest_meta: dict, ocr_text: str) -> str:
    meta = ingest_meta.get("meta", {})
    attachment_filename = ingest_meta.get("attachment_filename")

    for dest in meta.get("destinations", []):
        name = str(dest).strip()
        if name:
            return name

    if attachment_filename:
        stem = Path(attachment_filename).stem
        if stem and stem.lower() not in _GENERIC_FILENAMES:
            return stem

    for line in ocr_text.splitlines():
        line = line.strip()
        if line and len(line) <= 80:
            return line

    return datetime.now(timezone.utc).strftime("%Y-%m-%d_%H%M%S")


async def _run_ocr(
    doc: Document, stage: StageDefinition, filesystem, ollama_base_url: str
) -> tuple[dict, str, str]:
    """Returns (updated_stage_data, title, new_png_path)."""
    from adapters.outbound.ollama import generate_vision

    if not doc.png_path:
        raise ValueError("No PNG path set on document")

    if stage.prompt:
        from jinja2 import Template
        raw_template = Path(stage.prompt).read_text(encoding="utf-8")
        document_context = doc.stage_data.get("_ingest", {}).get("document_context", "")
        prompt_text = Template(raw_template).render(document_context=document_context)
    else:
        prompt_text = ""
    image_bytes = Path(doc.png_path).read_bytes()

    ocr_text = await generate_vision(
        ollama_base_url, stage.model, prompt_text, image_bytes
    )
    if not ocr_text:
        ocr_text = "(no text recognised)"
    logger.info("OCR for %s: %d chars", doc.id[:8], len(ocr_text))

    output_field = "ocr_raw"
    if stage.outputs:
        for o in stage.outputs:
            if o.get("type") == "text":
                output_field = o.get("field", "ocr_raw")
                break

    stage_data = dict(doc.stage_data)
    stage_data[stage.name] = {output_field: ocr_text}

    if doc.title:
        return stage_data, doc.title, doc.png_path

    title = _extract_title(stage_data.get("_ingest", {}), ocr_text)
    safe_title = _sanitize(title)

    try:
        new_png_path = filesystem.rename_png(doc.png_path, safe_title)
    except Exception as exc:
        logger.warning("PNG rename failed: %s", exc)
        new_png_path = doc.png_path

    return stage_data, title, new_png_path


async def _run_llm_text(
    doc: Document, stage: StageDefinition, ollama_base_url: str, db=None
) -> dict:
    """Returns updated stage_data dict."""
    import json
    import re
    from jinja2 import Template
    from adapters.outbound.ollama import generate_text, GenerationCancelled as _GC  # noqa: F401

    # Find input value — search all stage_data dicts for stage.input field
    input_text = ""
    if stage.input:
        for _, sdata in doc.stage_data.items():
            if isinstance(sdata, dict) and stage.input in sdata:
                input_text = sdata[stage.input]
                break

    # Render Jinja2 prompt
    prompt_text = ""
    if stage.prompt:
        raw_template = Path(stage.prompt).read_text(encoding="utf-8")
        existing = doc.stage_data.get(stage.name, {})
        document_context = doc.stage_data.get("_ingest", {}).get("document_context", "")
        linked_context = ""
        linked_context_name = ""
        if db is not None and doc.context_ref:
            for e in await db.list_contexts():
                if e.get("id") == doc.context_ref:
                    linked_context = e.get("text", "").strip()
                    linked_context_name = e.get("name", "").strip()
                    break
        qa_history = existing.get("qa_history", [])
        free_prompt = existing.get("free_prompt", "")
        previous_output = existing.get("clarified_text", "") if qa_history else ""
        prompt_text = Template(raw_template).render(
            qa_history=qa_history,
            document_context=document_context,
            linked_context=linked_context,
            linked_context_name=linked_context_name,
            free_prompt=free_prompt,
            previous_output=previous_output,
        )

    async def _is_stopped():
        if db is None:
            return False
        current = await db.get(doc.id)
        return current is not None and current.stage_state != "running"

    from adapters.outbound import streams as _streams

    _q = _streams.get_queue(doc.id)

    async def _on_chunk(chunk: str):
        await _q.put({"type": "token", "text": chunk})

    logger.debug("Prompt for stage '%s' doc %s:\n%s", stage.name, doc.id[:8], prompt_text)
    logger.debug("Input for stage '%s' doc %s:\n%s", stage.name, doc.id[:8], input_text)
    image_bytes = Path(doc.png_path).read_bytes() if stage.vision and doc.png_path else None
    raw_response = await generate_text(
        ollama_base_url,
        stage.model,
        prompt_text,
        input_text,
        is_stopped=_is_stopped,
        on_chunk=_on_chunk,
        image_bytes=image_bytes,
    )

    logger.debug("Response for stage '%s' doc %s:\n%s", stage.name, doc.id[:8], raw_response)

    # Parse response — clarify uses XML tags, other stages use JSON
    if "<clarified_text>" in raw_response:
        def _extract(tag: str) -> str:
            m = re.search(rf"<{tag}>(.*?)</{tag}>", raw_response, re.DOTALL)
            return m.group(1).strip() if m else ""
        clarified = re.sub(r"<!--.*?-->", "", _extract("clarified_text"), flags=re.DOTALL).strip()
        def _parse_questions(raw: str) -> list:
            try:
                result = json.loads(raw or "[]")
                return result if isinstance(result, list) else []
            except json.JSONDecodeError:
                return []
        parsed = {
            "clarified_text": clarified,
            "confidence": _extract("confidence") or "medium",
            "clarification_requests": _parse_questions(_extract("questions")),
            "document_context_update": _extract("document_context_update"),
            "linked_context_update": _extract("linked_context_update"),
        }
    else:
        cleaned = re.sub(r"^```(?:json)?\s*", "", raw_response.strip())
        cleaned = re.sub(r"\s*```$", "", cleaned)
        if not cleaned:
            logger.error("Empty response for stage '%s' doc %s", stage.name, doc.id[:8])
            raise ValueError(f"Empty LLM response for stage '{stage.name}'")
        try:
            parsed = json.loads(cleaned)
        except json.JSONDecodeError:
            logger.error("Failed to parse response for stage '%s':\n%s", stage.name, cleaned[:500])
            raise

    # Build new stage entry, preserving user inputs across re-runs
    existing = doc.stage_data.get(stage.name, {})
    new_entry: dict = {}
    if existing.get("qa_history"):
        new_entry["qa_history"] = existing["qa_history"]
    if existing.get("free_prompt"):
        new_entry["free_prompt"] = existing["free_prompt"]

    if stage.output and stage.output in parsed:
        new_entry[stage.output] = parsed[stage.output]
    if stage.outputs:
        for o in stage.outputs:
            field = o.get("field")
            if field and field in parsed:
                new_entry[field] = parsed[field]
    if "clarification_requests" in parsed:
        new_entry["clarification_requests"] = parsed["clarification_requests"]
    if "confidence" in parsed:
        new_entry["confidence"] = parsed["confidence"]
    _NULL_VALS = {"none", "null", "n/a", "nothing", "no updates", "no new information", ""}
    for _field in ("document_context_update", "linked_context_update"):
        val = parsed.get(_field, "")
        if isinstance(val, str):
            val = val.strip()
        if val and val.lower() not in _NULL_VALS:
            new_entry[_field] = val

    stage_data = dict(doc.stage_data)
    stage_data[stage.name] = new_entry
    logger.info(
        "LLM text '%s' for %s: keys=%s", stage.name, doc.id[:8], list(new_entry.keys())
    )
    return stage_data


async def _run_embed(
    doc: Document, stage: StageDefinition, ollama_base_url: str, db
) -> None:
    from adapters.outbound.ollama import generate_embed
    from adapters.outbound import qdrant as _qdrant
    from adapters.outbound import open_webui as _open_webui

    input_text = ""
    if stage.input:
        for _, sd in doc.stage_data.items():
            if isinstance(sd, dict) and stage.input in sd:
                input_text = sd[stage.input]
                break

    if not input_text:
        raise ValueError(
            f"Embed stage '{stage.name}': no text found for input '{stage.input}'"
        )

    # Collect metadata
    all_data: dict = {}
    for sd in doc.stage_data.values():
        if isinstance(sd, dict):
            all_data.update(sd)
    metadata: dict = {}
    for field in stage.metadata_fields or []:
        if field in all_data:
            metadata[field] = all_data[field]
    if doc.title:
        metadata["title"] = doc.title
    if doc.date_month:
        metadata["date_month"] = doc.date_month

    now_str = datetime.now(timezone.utc).isoformat()

    for dest in stage.destinations or []:
        dtype = dest.get("type")

        if dtype == "qdrant":
            vector = await generate_embed(ollama_base_url, stage.model, input_text)
            payload = {"doc_id": doc.id, "text": input_text, **metadata}
            await _qdrant.upsert(
                dest.get("url", ""),
                dest.get("collection", "remarkable"),
                doc.id,
                vector,
                payload,
                dest.get("api_key") or None,
            )
            await db.append_event(
                doc.id, stage.name, "synced", now_str, {"destination": "qdrant"}
            )
            logger.info(
                "Embedded %s into qdrant/%s", doc.id[:8], dest.get("collection")
            )

        elif dtype == "open_webui":
            await _open_webui.upsert(
                base_url=dest.get("url", ""),
                api_key=dest.get("api_key", ""),
                knowledge_id=dest.get("knowledge_id", ""),
                doc_id=doc.id,
                title=doc.title or doc.id,
                text=input_text,
                metadata=metadata,
            )
            await db.append_event(
                doc.id, stage.name, "synced", now_str, {"destination": "open_webui"}
            )

        else:
            logger.warning(
                "Unknown embed destination type '%s' for doc %s", dtype, doc.id[:8]
            )


async def _was_stopped(doc_id: str, db) -> bool:
    """Return True if the document was externally stopped while the worker was running."""
    current = await db.get(doc_id)
    if current and current.stage_state != "running":
        logger.info("Doc %s was stopped externally — discarding result", doc_id[:8])
        return True
    return False


async def _process_document(
    doc: Document,
    stage: StageDefinition,
    db,
    filesystem,
    ollama_base_url: str,
    config: PipelineConfig,
):
    now_str = datetime.now(timezone.utc).isoformat()
    await db.update(set_running(doc, now_str))
    await db.append_event(doc.id, stage.name, "started", now_str)

    try:
        if stage.type == "computer_vision":
            stage_data, title, png_path = await _run_ocr(
                doc, stage, filesystem, ollama_base_url
            )
            if await _was_stopped(doc.id, db):
                return
            now_str = datetime.now(timezone.utc).isoformat()
            updated = replace(
                doc, stage_data=stage_data, title=title, png_path=png_path
            )
            next_stage = config.next_stage(stage.name)
            updated = (
                advance(updated, next_stage.name, now_str)
                if next_stage
                else set_done(updated, now_str)
            )
            await db.update(updated)
            await db.append_event(doc.id, stage.name, "completed", now_str)
            logger.info("Doc %s → %s", doc.id[:8], updated.current_stage)

        elif stage.type == "llm_text":
            if not _check_start_if(doc, stage):
                await db.update(set_waiting(doc, now_str))
                return
            stage_data = await _run_llm_text(doc, stage, ollama_base_url, db=db)
            from adapters.outbound import streams as _streams

            if await _was_stopped(doc.id, db):
                await _streams.put_done(doc.id)
                return
            now_str = datetime.now(timezone.utc).isoformat()
            if not _check_continue_if(stage_data, stage):
                # Park for human review at current stage
                updated = replace(
                    doc,
                    stage_data=stage_data,
                    stage_state="waiting",
                    updated_at=now_str,
                )
                await db.update(updated)
                await db.append_event(doc.id, stage.name, "awaiting_review", now_str)
                await _streams.put_done(doc.id)
                return
            updated = replace(doc, stage_data=stage_data)
            next_stage = config.next_stage(stage.name)
            updated = (
                advance(updated, next_stage.name, now_str)
                if next_stage
                else set_done(updated, now_str)
            )
            await db.update(updated)
            await db.append_event(doc.id, stage.name, "completed", now_str)
            await _streams.put_done(doc.id)
            logger.info("Doc %s → %s", doc.id[:8], updated.current_stage)

        elif stage.type == "embed":
            await _run_embed(doc, stage, ollama_base_url, db)
            now_str = datetime.now(timezone.utc).isoformat()
            next_stage = config.next_stage(stage.name)
            updated = (
                advance(doc, next_stage.name, now_str)
                if next_stage
                else set_done(doc, now_str)
            )
            await db.update(updated)
            await db.append_event(doc.id, stage.name, "completed", now_str)
            logger.info("Doc %s → %s", doc.id[:8], updated.current_stage)

    except Exception as exc:
        from adapters.outbound.ollama import GenerationCancelled
        from adapters.outbound import streams as _streams

        await _streams.put_done(doc.id)
        if isinstance(exc, GenerationCancelled):
            logger.info(
                "Doc %s stream cancelled mid-flight (stopped externally)", doc.id[:8]
            )
            return  # doc state already set to error by the stop endpoint
        logger.error(
            "Stage '%s' failed for %s: %s", stage.name, doc.id[:8], exc, exc_info=True
        )
        now_str = datetime.now(timezone.utc).isoformat()
        await db.append_event(
            doc.id, stage.name, "failed", now_str, {"error": traceback.format_exc()}
        )

        failures = await db.count_failures(doc.id, stage.name)
        if failures < 3:
            backoff = 2**failures  # 2s, 4s, 8s
            logger.info(
                "Will retry %s/%s in %ds (attempt %d/3)",
                doc.id[:8],
                stage.name,
                backoff,
                failures,
            )
            await asyncio.sleep(backoff)
            await db.update(replace(doc, stage_state="pending", updated_at=now_str))
        else:
            logger.error("Doc %s exhausted retries for '%s'", doc.id[:8], stage.name)
            await db.update(set_error(doc, now_str))


async def run_worker(config: PipelineConfig, db, vault_path: str, ollama_base_url: str):
    from adapters.outbound import filesystem
    from adapters.outbound.ollama import unload_model

    logger.info("Worker started")

    while True:
        try:
            processed_any = False

            for stage in config.stages:
                if stage.type not in _HANDLED_TYPES:
                    continue

                docs = await db.get_pending(stage.name)
                if not docs:
                    continue

                logger.info("Stage '%s': processing %d doc(s)", stage.name, len(docs))

                stage_max_concurrent = (
                    stage.max_concurrent
                    if stage.max_concurrent is not None
                    else config.max_concurrent
                )
                sem = asyncio.Semaphore(stage_max_concurrent)

                async def _run(doc, _sem=sem):
                    async with _sem:
                        await _process_document(
                            doc, stage, db, filesystem, ollama_base_url, config
                        )

                await asyncio.gather(*(_run(doc) for doc in docs))

                if stage.model:
                    await unload_model(ollama_base_url, stage.model)

                processed_any = True
                break  # restart from earliest stage so OCR always takes priority

            if not processed_any:
                await asyncio.sleep(5)

        except asyncio.CancelledError:
            logger.info("Worker shutting down")
            raise
        except Exception as exc:
            logger.error("Worker loop error: %s", exc, exc_info=True)
            await asyncio.sleep(5)
