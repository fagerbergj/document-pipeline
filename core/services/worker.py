from __future__ import annotations

import asyncio
import logging
import traceback
from dataclasses import replace
from datetime import datetime, timezone
from pathlib import Path
from typing import Optional

import uuid as _uuid
from dataclasses import replace as _dc_replace

from core.domain.document import (
    Document,
    advance,
    set_done,
    set_error,
    set_running,
    set_waiting,
)
from core.domain.job import Job
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
        ingest = doc.stage_data.get("_ingest", {})
        if not ingest.get("document_context") and not doc.context_ref:
            return False
    return True


def _check_skip_if(doc: Document, stage: StageDefinition) -> bool:
    """Return True if the stage should be skipped based on document metadata."""
    rules = stage.skip_if or {}
    if "file_type" in rules:
        file_type = doc.stage_data.get("_ingest", {}).get("file_type", "")
        return file_type in rules["file_type"]
    return False


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
) -> tuple[dict, str, str, dict, dict]:
    """Returns (updated_stage_data, title, new_png_path, input_ref, output_ref)."""
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

    input_refs = [{"type": "png"}]
    output_refs = [{"stage": stage.name, "field": output_field}]

    if doc.title:
        return stage_data, doc.title, doc.png_path, input_refs, output_refs

    title = _extract_title(stage_data.get("_ingest", {}), ocr_text)
    safe_title = _sanitize(title)

    try:
        new_png_path = filesystem.rename_png(doc.png_path, safe_title)
    except Exception as exc:
        logger.warning("PNG rename failed: %s", exc)
        new_png_path = doc.png_path

    return stage_data, title, new_png_path, input_refs, output_refs


async def _run_llm_text(
    doc: Document, stage: StageDefinition, ollama_base_url: str, db=None, job=None
) -> tuple[dict, str, dict, dict]:
    """Returns (updated_stage_data, raw_response, input_ref, output_ref)."""
    import json
    import re
    from jinja2 import Template
    from adapters.outbound.ollama import generate_text, GenerationCancelled as _GC  # noqa: F401

    # Find input value — search all stage_data dicts for stage.input field
    input_text = ""
    input_src_stage = None
    if stage.input:
        for src_stage, sdata in doc.stage_data.items():
            if isinstance(sdata, dict) and stage.input in sdata:
                input_text = sdata[stage.input]
                input_src_stage = src_stage
                break

    input_refs = [
        {"stage": input_src_stage, "field": stage.input}
        if input_src_stage else {"type": "unknown"}
    ]

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
        # Read qa_history from job row if available, else fall back to stage_data
        qa_history = job.qa_rounds if job is not None else existing.get("qa_history", [])
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
    # qa_history now lives in jobs.qa_rounds — keep in stage_data only as fallback for
    # pre-migration documents that don't have a job row yet
    if job is None and existing.get("qa_history"):
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

    # Build output_refs
    if stage.output:
        output_refs = [{"stage": stage.name, "field": stage.output}]
    elif stage.outputs:
        output_refs = [{"stage": stage.name, "field": o["field"]} for o in stage.outputs if o.get("field")]
    else:
        output_refs = []

    logger.info(
        "LLM text '%s' for %s: keys=%s", stage.name, doc.id[:8], list(new_entry.keys())
    )
    return stage_data, raw_response, input_refs, output_refs


async def _run_embed(
    doc: Document, stage: StageDefinition, ollama_base_url: str, db, job=None
) -> tuple[list, list]:
    """Returns (input_refs, output_refs)."""
    from adapters.outbound.ollama import generate_embed
    from adapters.outbound import qdrant as _qdrant
    from adapters.outbound import open_webui as _open_webui

    input_text = ""
    input_src_stage = None
    if stage.input:
        for src_stage, sd in doc.stage_data.items():
            if isinstance(sd, dict) and stage.input in sd:
                input_text = sd[stage.input]
                input_src_stage = src_stage
                break

    if not input_text:
        raise ValueError(
            f"Embed stage '{stage.name}': no text found for input '{stage.input}'"
        )

    # Truncate to ~32k chars — nomic-embed-text context limit is 8192 tokens (~4 chars/token)
    _MAX_EMBED_CHARS = 32_000
    if len(input_text) > _MAX_EMBED_CHARS:
        logger.warning(
            "Doc %s: input_text truncated from %d to %d chars for embedding",
            doc.id[:8], len(input_text), _MAX_EMBED_CHARS,
        )
        input_text = input_text[:_MAX_EMBED_CHARS]

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

    input_refs = [
        {"stage": input_src_stage, "field": stage.input}
        if input_src_stage else {"type": "unknown"}
    ]
    output_refs = []
    now_str = datetime.now(timezone.utc).isoformat()

    # Determine whether to embed the image: job config > doc field > dest config
    should_embed_image = (
        (job.get_embed_image() if job is not None else False)
        or doc.embed_image
        or False
    )

    for dest in stage.destinations or []:
        dtype = dest.get("type")

        if dtype == "qdrant":
            text_vector = await generate_embed(ollama_base_url, stage.model, input_text)

            dest_embed_image = should_embed_image or dest.get("embed_image", False)
            image_vector = None
            if dest_embed_image and doc.png_path:
                image_model = dest.get("image_model") or stage.model
                try:
                    image_bytes = Path(doc.png_path).read_bytes()
                    image_vector = await generate_embed(
                        ollama_base_url, image_model, "", image_bytes=image_bytes
                    )
                    logger.info("Image embed ok for %s (model=%s)", doc.id[:8], image_model)
                except Exception as exc:
                    logger.warning(
                        "Image embed failed for %s, continuing text-only: %s",
                        doc.id[:8], exc,
                    )
            elif dest_embed_image and not doc.png_path:
                logger.warning("embed_image=true but doc %s has no image", doc.id[:8])

            payload = {"doc_id": doc.id, "text": input_text, **metadata}
            await _qdrant.upsert(
                dest.get("url", ""),
                dest.get("collection", "remarkable"),
                doc.id,
                text_vector,
                payload,
                dest.get("api_key") or None,
                image_vector=image_vector,
            )
            await db.append_event(
                doc.id, stage.name, "synced", now_str, {"destination": "qdrant"}
            )
            output_refs.append({"type": "qdrant", "collection": dest.get("collection", "remarkable")})
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
            output_refs.append({"type": "open_webui", "knowledge_id": dest.get("knowledge_id", "")})

        else:
            logger.warning(
                "Unknown embed destination type '%s' for doc %s", dtype, doc.id[:8]
            )

    return input_refs, output_refs


async def _was_stopped(doc_id: str, db) -> bool:
    """Return True if the document was externally stopped while the worker was running."""
    current = await db.get(doc_id)
    if current and current.stage_state != "running":
        logger.info("Doc %s was stopped externally — discarding result", doc_id[:8])
        return True
    return False


def _make_job(doc: Document, stage_name: str, now_str: str, config: dict = None) -> Job:
    return Job(
        id=str(_uuid.uuid4()),
        document_id=doc.id,
        stage=stage_name,
        state="pending",
        config=config or {},
        created_at=now_str,
        updated_at=now_str,
    )


async def _process_document(
    doc: Document,
    stage: StageDefinition,
    db,
    filesystem,
    ollama_base_url: str,
    config: PipelineConfig,
):
    now_str = datetime.now(timezone.utc).isoformat()

    # Ensure a job row exists for this stage; create one if missing
    job = await db.get_job(doc.id, stage.name)
    if job is None:
        job = _make_job(doc, stage.name, now_str)
        await db.upsert_job(job)

    await db.update(set_running(doc, now_str))
    await db.upsert_job(_dc_replace(job, state="running", updated_at=now_str))
    await db.append_event(doc.id, stage.name, "started", now_str)

    try:
        if stage.type == "computer_vision":
            if _check_skip_if(doc, stage):
                # Text file — bypass vision model, use raw_text as output
                raw_text = doc.stage_data.get("_ingest", {}).get("raw_text", "")
                output_field = (
                    stage.outputs[0]["field"] if stage.outputs
                    else stage.output or "ocr_raw"
                )
                stage_data = {**doc.stage_data, stage.name: {output_field: raw_text}}
                title = _extract_title(doc.stage_data.get("_ingest", {}), raw_text) or doc.title
                fresh = await db.get(doc.id)
                if fresh and fresh.title:
                    title = fresh.title
                now_str = datetime.now(timezone.utc).isoformat()
                updated = replace(doc, stage_data=stage_data, title=title)
                next_stage = config.next_stage(stage.name)
                updated = (
                    advance(updated, next_stage.name, now_str)
                    if next_stage
                    else set_done(updated, now_str)
                )
                await db.update(updated)
                skip_input_refs = [{"type": "text", "stage": "_ingest", "field": "raw_text"}]
                skip_output_refs = [{"stage": stage.name, "field": output_field}]
                await db.upsert_job(_dc_replace(
                    job, state="done",
                    input_refs=skip_input_refs, output_refs=skip_output_refs,
                    updated_at=now_str,
                ))
                if next_stage:
                    await db.upsert_job(_make_job(updated, next_stage.name, now_str))
                await db.append_event(doc.id, stage.name, "skipped", now_str)
                logger.info("Doc %s — skipped %s (text upload)", doc.id[:8], stage.name)
                return

            stage_data, title, png_path, input_refs, output_refs = await _run_ocr(
                doc, stage, filesystem, ollama_base_url
            )
            if await _was_stopped(doc.id, db):
                return
            # Re-fetch to pick up any title the user set while OCR was running
            fresh = await db.get(doc.id)
            if fresh and fresh.title:
                title = fresh.title
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
            await db.upsert_job(_dc_replace(
                job, state="done",
                input_refs=input_refs, output_refs=output_refs,
                updated_at=now_str,
            ))
            if next_stage:
                await db.upsert_job(_make_job(updated, next_stage.name, now_str))
            await db.append_event(doc.id, stage.name, "completed", now_str)
            logger.info("Doc %s → %s", doc.id[:8], updated.current_stage)

        elif stage.type == "llm_text":
            if not _check_start_if(doc, stage):
                await db.update(set_waiting(doc, now_str))
                await db.upsert_job(_dc_replace(job, state="waiting", updated_at=now_str))
                return
            stage_data, raw_response, input_refs, output_refs = await _run_llm_text(
                doc, stage, ollama_base_url, db=db, job=job
            )
            from adapters.outbound import streams as _streams

            if await _was_stopped(doc.id, db):
                await _streams.put_done(doc.id)
                return
            now_str = datetime.now(timezone.utc).isoformat()
            new_llm_log = list(job.llm_log) + [raw_response]
            if not _check_continue_if(stage_data, stage):
                # Park for human review at current stage
                updated = replace(
                    doc,
                    stage_data=stage_data,
                    stage_state="waiting",
                    updated_at=now_str,
                )
                await db.update(updated)
                await db.upsert_job(_dc_replace(
                    job, state="waiting",
                    input_refs=input_refs, llm_log=new_llm_log,
                    updated_at=now_str,
                ))
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
            await db.upsert_job(_dc_replace(
                job, state="done",
                input_refs=input_refs, output_refs=output_refs,
                llm_log=new_llm_log,
                updated_at=now_str,
            ))
            if next_stage:
                await db.upsert_job(_make_job(updated, next_stage.name, now_str))
            await db.append_event(doc.id, stage.name, "completed", now_str)
            await _streams.put_done(doc.id)
            logger.info("Doc %s → %s", doc.id[:8], updated.current_stage)

        elif stage.type == "embed":
            input_refs, output_refs = await _run_embed(doc, stage, ollama_base_url, db, job=job)
            now_str = datetime.now(timezone.utc).isoformat()
            next_stage = config.next_stage(stage.name)
            updated = (
                advance(doc, next_stage.name, now_str)
                if next_stage
                else set_done(doc, now_str)
            )
            await db.update(updated)
            await db.upsert_job(_dc_replace(
                job, state="done",
                input_refs=input_refs, output_refs=output_refs,
                updated_at=now_str,
            ))
            if next_stage:
                await db.upsert_job(_make_job(updated, next_stage.name, now_str))
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
            await db.upsert_job(_dc_replace(job, state="pending", updated_at=now_str))
        else:
            logger.error("Doc %s exhausted retries for '%s'", doc.id[:8], stage.name)
            await db.update(set_error(doc, now_str))
            await db.upsert_job(_dc_replace(job, state="error", updated_at=now_str))


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
