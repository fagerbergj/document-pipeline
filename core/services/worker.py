from __future__ import annotations

import asyncio
import json as _json
import logging
import traceback
from dataclasses import replace
from datetime import datetime, timezone
from pathlib import Path
from typing import Optional

import uuid as _uuid
from dataclasses import replace as _dc_replace

from core.domain.document import Document
from core.domain.job import Job
from core.domain.pipeline import PipelineConfig, StageDefinition

logger = logging.getLogger(__name__)

_HANDLED_TYPES = {"computer_vision", "llm_text", "embed"}
_GENERIC_FILENAMES = {"remarkable", "untitled", "image", "attachment", "document"}
_CONFIDENCE_LEVELS = {"low": 0, "medium": 1, "high": 2}


def _check_start_if(doc: Document, stage: StageDefinition) -> bool:
    """Return False if start conditions are not yet met."""
    rules = stage.start_if or {}
    if stage.require_context or rules.get("context_provided"):
        if not doc.additional_context and not doc.linked_contexts:
            return False
    return True


def _check_skip_if(doc: Document, stage: StageDefinition, ingest_meta: dict) -> bool:
    """Return True if the stage should be skipped based on document metadata."""
    rules = stage.skip_if or {}
    if "file_type" in rules:
        file_type = ingest_meta.get("file_type", "")
        return file_type in rules["file_type"]
    return False


def _check_continue_if(stage_data: dict, stage: StageDefinition) -> bool:
    """Return True if LLM output satisfies continue rules (auto-advance without review)."""
    rules = stage.continue_if
    if not rules:
        return True
    sdata = stage_data.get(stage.name, {})
    for rule in rules:
        if "confidence" in rule:
            required = _CONFIDENCE_LEVELS.get(rule["confidence"], 2)
            actual = _CONFIDENCE_LEVELS.get(sdata.get("confidence", "low"), 0)
            if actual >= required:
                return True
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


def _now_str() -> str:
    return datetime.now(timezone.utc).isoformat()


def _make_run(
    inputs: list,
    outputs: list,
    confidence: str,
    questions: list,
    suggestions: dict,
) -> dict:
    now = _now_str()
    return {
        "inputs": inputs,
        "outputs": outputs,
        "confidence": confidence,
        "questions": questions,
        "suggestions": suggestions,
        "created_at": now,
        "updated_at": now,
    }


async def _advance_pipeline(job: Job, config: PipelineConfig, db, now: str) -> Optional[Job]:
    """Upsert the next stage job to pending. Returns the new job if created."""
    stage_names = [s.name for s in config.stages]
    if job.stage not in stage_names:
        return None
    idx = stage_names.index(job.stage)
    if idx + 1 >= len(stage_names):
        return None
    next_stage_name = stage_names[idx + 1]
    next_stage_def = config.get_stage(next_stage_name)
    options = {"require_context": bool(getattr(next_stage_def, "require_context", False))} if next_stage_def else {}

    existing = await db.get_job(job.document_id, next_stage_name)
    if existing is not None:
        await db.update_job_status(existing.id, "pending", now)
        return existing
    else:
        next_job = Job(
            id=str(_uuid.uuid4()),
            document_id=job.document_id,
            stage=next_stage_name,
            status="pending",
            options=options,
            created_at=now,
            updated_at=now,
        )
        await db.upsert_job(next_job)
        return next_job


async def _get_ingest_meta(doc: Document, db) -> dict:
    """Fetch stored ingest metadata for a document."""
    raw = await db.kv_get(f"ingest_meta:{doc.id}")
    return _json.loads(raw) if raw else {}


async def _get_stage_data(doc: Document, db) -> dict:
    """Collect stage outputs from all completed job runs for this document."""
    jobs = await db.list_jobs_for_document(doc.id)
    stage_data: dict = {}
    for j in jobs:
        if j.runs and j.status in ("done", "waiting"):
            latest = j.runs[-1]
            outputs = {field["field"]: field["text"] for field in latest.get("outputs", []) if field.get("field")}
            if outputs:
                stage_data[j.stage] = outputs
    return stage_data


async def _run_ocr(
    doc: Document, stage: StageDefinition, ingest_meta: dict, filesystem, ollama_base_url: str
) -> tuple[list, list, str]:
    """Returns (input_items, output_items, title)."""
    from adapters.outbound.ollama import generate_vision

    if not doc.png_path:
        raise ValueError("No PNG path set on document")

    if stage.prompt:
        from jinja2 import Template
        raw_template = Path(stage.prompt).read_text(encoding="utf-8")
        prompt_text = Template(raw_template).render(additional_context=doc.additional_context)
    else:
        prompt_text = ""

    image_bytes = Path(doc.png_path).read_bytes()
    ocr_text = await generate_vision(ollama_base_url, stage.model, prompt_text, image_bytes)
    if not ocr_text:
        ocr_text = "(no text recognised)"
    logger.info("OCR for %s: %d chars", doc.id[:8], len(ocr_text))

    output_field = "ocr_raw"
    if stage.outputs:
        for o in stage.outputs:
            if o.get("type") == "text":
                output_field = o.get("field", "ocr_raw")
                break

    input_items = [{"field": None, "text": "(image)"}]
    output_items = [{"field": output_field, "text": ocr_text}]

    if doc.title:
        return input_items, output_items, doc.title

    title = _extract_title(ingest_meta, ocr_text)
    return input_items, output_items, title


async def _run_llm_text(
    doc: Document, stage: StageDefinition, job: Job, stage_data: dict,
    ollama_base_url: str, db,
) -> tuple[list, list, str, str, list, dict]:
    """Returns (input_items, output_items, raw_response, confidence, questions, suggestions)."""
    import json
    import re
    from jinja2 import Template
    from adapters.outbound.ollama import generate_text

    input_text = ""
    input_field = None
    if stage.input:
        for src_stage, sdata in stage_data.items():
            if isinstance(sdata, dict) and stage.input in sdata:
                input_text = sdata[stage.input]
                input_field = stage.input
                break

    prompt_text = ""
    if stage.prompt:
        raw_template = Path(stage.prompt).read_text(encoding="utf-8")
        linked_context = ""
        linked_context_name = ""
        if db is not None and doc.linked_contexts:
            for ctx_id in doc.linked_contexts:
                entry = await db.get_context(ctx_id)
                if entry:
                    linked_context = entry.get("text", "").strip()
                    linked_context_name = entry.get("name", "").strip()
                    break

        # Build qa_history from job runs
        qa_history = []
        for run in job.runs:
            answers = [{"segment": q["segment"], "answer": q.get("answer", "")} for q in run.get("questions", []) if q.get("answer")]
            if answers:
                qa_history.append({"answers": answers, "free_prompt": ""})

        free_prompt = ""
        previous_output = ""
        if job.runs:
            last_run = job.runs[-1]
            previous_outputs = last_run.get("outputs", [])
            if previous_outputs:
                previous_output = previous_outputs[0].get("text", "")

        prompt_text = Template(raw_template).render(
            qa_history=qa_history,
            additional_context=doc.additional_context,
            linked_context=linked_context,
            linked_context_name=linked_context_name,
            free_prompt=free_prompt,
            previous_output=previous_output,
        )

    async def _is_stopped():
        if db is None:
            return False
        current_job = await db.get_job_by_id(job.id)
        return current_job is not None and current_job.status != "running"

    from adapters.outbound import streams as _streams
    _q = _streams.get_queue(doc.id)

    async def _on_chunk(chunk: str):
        await _q.put({"type": "token", "text": chunk})

    image_bytes = Path(doc.png_path).read_bytes() if stage.vision and doc.png_path else None
    raw_response = await generate_text(
        ollama_base_url, stage.model, prompt_text, input_text,
        is_stopped=_is_stopped, on_chunk=_on_chunk, image_bytes=image_bytes,
    )

    # Parse response
    _NULL_VALS = {"none", "null", "n/a", "nothing", "no updates", "no new information", ""}

    if "<clarified_text>" in raw_response:
        def _extract(tag: str) -> str:
            import re as _re
            m = _re.search(rf"<{tag}>(.*?)</{tag}>", raw_response, _re.DOTALL)
            return m.group(1).strip() if m else ""
        clarified = re.sub(r"<!--.*?-->", "", _extract("clarified_text"), flags=re.DOTALL).strip()
        def _parse_questions(raw: str) -> list:
            try:
                result = json.loads(raw or "[]")
                return result if isinstance(result, list) else []
            except json.JSONDecodeError:
                return []
        confidence = _extract("confidence") or "medium"
        raw_questions = _parse_questions(_extract("questions"))
        questions = [{"segment": q.get("segment", ""), "question": q.get("question", ""), "answer": None}
                     for q in raw_questions if isinstance(q, dict)]

        doc_ctx_update = _extract("document_context_update").strip()
        linked_ctx_update = _extract("linked_context_update").strip()
        suggestions = {
            "additional_context": "" if doc_ctx_update.lower() in _NULL_VALS else doc_ctx_update,
            "linked_context": "" if linked_ctx_update.lower() in _NULL_VALS else linked_ctx_update,
            "linked_context_id": doc.linked_contexts[0] if doc.linked_contexts else None,
        }

        output_field = stage.output or (stage.outputs[0].get("field") if stage.outputs else None)
        output_items = [{"field": output_field, "text": clarified}] if output_field else []
    else:
        cleaned = re.sub(r"^```(?:json)?\s*", "", raw_response.strip())
        cleaned = re.sub(r"\s*```$", "", cleaned)
        if not cleaned:
            raise ValueError(f"Empty LLM response for stage '{stage.name}'")
        parsed = json.loads(cleaned)
        confidence = str(parsed.get("confidence", "medium"))
        questions = []
        suggestions = {"additional_context": "", "linked_context": "", "linked_context_id": None}

        output_items = []
        if stage.output and stage.output in parsed:
            output_items.append({"field": stage.output, "text": str(parsed[stage.output])})
        if stage.outputs:
            for o in stage.outputs:
                field = o.get("field")
                if field and field in parsed:
                    val = parsed[field]
                    output_items.append({"field": field, "text": val if isinstance(val, str) else json.dumps(val, ensure_ascii=False)})

    input_items = [{"field": input_field, "text": input_text}] if input_field else []
    return input_items, output_items, raw_response, confidence, questions, suggestions


async def _run_embed(
    doc: Document, stage: StageDefinition, stage_data: dict,
    job: Job, ollama_base_url: str, db,
) -> tuple[list, list]:
    """Returns (input_items, output_items)."""
    from adapters.outbound.ollama import generate_embed
    from adapters.outbound import qdrant as _qdrant
    from adapters.outbound import open_webui as _open_webui

    input_text = ""
    input_field = None
    if stage.input:
        for src_stage, sd in stage_data.items():
            if isinstance(sd, dict) and stage.input in sd:
                input_text = sd[stage.input]
                input_field = stage.input
                break

    if not input_text:
        raise ValueError(f"Embed stage '{stage.name}': no text found for input '{stage.input}'")

    _MAX_EMBED_CHARS = 32_000
    if len(input_text) > _MAX_EMBED_CHARS:
        logger.warning("Doc %s: truncating input from %d to %d chars", doc.id[:8], len(input_text), _MAX_EMBED_CHARS)
        input_text = input_text[:_MAX_EMBED_CHARS]

    all_data: dict = {}
    for sd in stage_data.values():
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

    should_embed_image = job.get_embed_image()
    output_items = []
    now_str = _now_str()

    for dest in stage.destinations or []:
        dtype = dest.get("type")
        if dtype == "qdrant":
            text_vector = await generate_embed(ollama_base_url, stage.model, input_text)
            image_vector = None
            if should_embed_image and doc.png_path:
                image_model = dest.get("image_model") or stage.model
                try:
                    image_bytes = Path(doc.png_path).read_bytes()
                    image_vector = await generate_embed(ollama_base_url, image_model, "", image_bytes=image_bytes)
                    logger.info("Image embed ok for %s", doc.id[:8])
                except Exception as exc:
                    logger.warning("Image embed failed for %s: %s", doc.id[:8], exc)

            payload = {"doc_id": doc.id, "text": input_text, **metadata}
            await _qdrant.upsert(
                dest.get("url", ""), dest.get("collection", "remarkable"),
                doc.id, text_vector, payload, dest.get("api_key") or None,
                image_vector=image_vector,
            )
            await db.append_event(doc.id, stage.name, "synced", now_str, {"destination": "qdrant"})
            output_items.append({"field": "qdrant_collection", "text": dest.get("collection", "remarkable")})

        elif dtype == "open_webui":
            await _open_webui.upsert(
                base_url=dest.get("url", ""), api_key=dest.get("api_key", ""),
                knowledge_id=dest.get("knowledge_id", ""), doc_id=doc.id,
                title=doc.title or doc.id, text=input_text, metadata=metadata,
            )
            await db.append_event(doc.id, stage.name, "synced", now_str, {"destination": "open_webui"})
            output_items.append({"field": "open_webui_knowledge", "text": dest.get("knowledge_id", "")})

    input_items = [{"field": input_field, "text": input_text}] if input_field else []
    return input_items, output_items


async def _save_stage_artifacts(
    stage: StageDefinition, output_items: list, job: Job, vault_path: str, db
) -> None:
    """Write each text output as a file in the vault and insert an artifact record."""
    if not stage.save_as_artifact:
        return
    now = _now_str()
    for item in output_items:
        text = item.get("text", "")
        if not text:
            continue
        field = item.get("field") or stage.name
        filename = f"{field}.md"
        artifact_id = str(_uuid.uuid4())
        dest_dir = Path(vault_path) / "artifacts" / artifact_id
        dest_dir.mkdir(parents=True, exist_ok=True)
        (dest_dir / filename).write_text(text, encoding="utf-8")
        await db.insert_artifact(
            document_id=job.document_id,
            filename=filename,
            content_type="text/markdown",
            created_job_id=job.id,
            now=now,
            artifact_id=artifact_id,
        )
        logger.info("Saved artifact %s for doc %s", filename, job.document_id[:8])


async def _was_stopped(job_id: str, db) -> bool:
    current = await db.get_job_by_id(job_id)
    if current and current.status != "running":
        logger.info("Job %s was stopped externally — discarding result", job_id[:8])
        return True
    return False


async def _process_job(
    job: Job,
    stage: StageDefinition,
    db,
    filesystem,
    ollama_base_url: str,
    config: PipelineConfig,
    vault_path: str = "",
):
    now = _now_str()
    doc = await db.get(job.document_id)
    if doc is None:
        logger.error("Job %s: document %s not found", job.id[:8], job.document_id[:8])
        return

    # Mark job running
    await db.update_job_status(job.id, "running", now)
    await db.append_event(doc.id, stage.name, "started", now)

    ingest_meta = await _get_ingest_meta(doc, db)
    stage_data = await _get_stage_data(doc, db)
    # Add ingest passthrough data (raw_text for text files)
    if ingest_meta:
        stage_data["_ingest"] = ingest_meta

    try:
        if stage.type == "computer_vision":
            if _check_skip_if(doc, stage, ingest_meta):
                # Text file — bypass vision, use raw_text
                raw_text = ingest_meta.get("raw_text", "")
                output_field = (stage.outputs[0]["field"] if stage.outputs else stage.output or "ocr_raw")
                input_items = [{"field": "raw_text", "text": raw_text}]
                output_items = [{"field": output_field, "text": raw_text}]
                title = doc.title or _extract_title(ingest_meta, raw_text)

                run = _make_run(input_items, output_items, "high", [], {"additional_context": "", "linked_context": "", "linked_context_id": None})
                await db.append_run(job.id, run, now)
                if title and not doc.title:
                    await db.update(replace(doc, title=title, updated_at=now))
                await db.update_job_status(job.id, "done", now)
                await db.append_event(doc.id, stage.name, "skipped", now)
                await _advance_pipeline(job, config, db, now)
                logger.info("Doc %s — skipped %s (text upload)", doc.id[:8], stage.name)
                return

            input_items, output_items, title = await _run_ocr(
                doc, stage, ingest_meta, filesystem, ollama_base_url
            )
            if await _was_stopped(job.id, db):
                return

            # Re-fetch title in case user set it while OCR was running
            fresh_doc = await db.get(doc.id)
            if fresh_doc and fresh_doc.title:
                title = fresh_doc.title

            now = _now_str()
            run = _make_run(input_items, output_items, "high", [], {"additional_context": "", "linked_context": "", "linked_context_id": None})
            await db.append_run(job.id, run, now)

            if title and not doc.title:
                await db.update(replace(doc, title=title, updated_at=now))

            await db.update_job_status(job.id, "done", now)
            await db.append_event(doc.id, stage.name, "completed", now)
            await _save_stage_artifacts(stage, output_items, job, vault_path, db)
            await _advance_pipeline(job, config, db, now)
            logger.info("Doc %s — %s done", doc.id[:8], stage.name)

        elif stage.type == "llm_text":
            if not _check_start_if(doc, stage):
                await db.update_job_status(job.id, "waiting", now)
                await db.append_event(doc.id, stage.name, "waiting_for_context", now)
                return

            input_items, output_items, raw_response, confidence, questions, suggestions = await _run_llm_text(
                doc, stage, job, stage_data, ollama_base_url, db
            )
            from adapters.outbound import streams as _streams
            if await _was_stopped(job.id, db):
                await _streams.put_done(doc.id)
                return

            now = _now_str()
            run = _make_run(input_items, output_items, confidence, questions, suggestions)
            await db.append_run(job.id, run, now)

            if not _check_continue_if({stage.name: {"confidence": confidence}}, stage) or questions:
                # Park for human review
                await db.update_job_status(job.id, "waiting", now)
                await db.append_event(doc.id, stage.name, "awaiting_review", now)
                await _streams.put_done(doc.id)
                return

            await db.update_job_status(job.id, "done", now)
            await db.append_event(doc.id, stage.name, "completed", now)
            await _save_stage_artifacts(stage, output_items, job, vault_path, db)
            await _streams.put_done(doc.id)
            await _advance_pipeline(job, config, db, now)
            logger.info("Doc %s — %s done", doc.id[:8], stage.name)

        elif stage.type == "embed":
            input_items, output_items = await _run_embed(doc, stage, stage_data, job, ollama_base_url, db)
            now = _now_str()
            run = _make_run(input_items, output_items, "high", [], {"additional_context": "", "linked_context": "", "linked_context_id": None})
            await db.append_run(job.id, run, now)
            await db.update_job_status(job.id, "done", now)
            await db.append_event(doc.id, stage.name, "completed", now)
            await _advance_pipeline(job, config, db, now)
            logger.info("Doc %s — %s done", doc.id[:8], stage.name)

    except Exception as exc:
        from adapters.outbound.ollama import GenerationCancelled
        from adapters.outbound import streams as _streams

        await _streams.put_done(doc.id)
        if isinstance(exc, GenerationCancelled):
            logger.info("Doc %s stream cancelled (stopped externally)", doc.id[:8])
            return

        logger.error("Stage '%s' failed for %s: %s", stage.name, doc.id[:8], exc, exc_info=True)
        now = _now_str()
        await db.append_event(doc.id, stage.name, "failed", now, {"error": traceback.format_exc()})

        failures = await db.count_failures(doc.id, stage.name)
        if failures < 3:
            backoff = 2 ** failures
            logger.info("Will retry %s/%s in %ds (attempt %d/3)", doc.id[:8], stage.name, backoff, failures)
            await asyncio.sleep(backoff)
            await db.update_job_status(job.id, "pending", now)
        else:
            logger.error("Doc %s exhausted retries for '%s'", doc.id[:8], stage.name)
            await db.update_job_status(job.id, "error", now)


async def run_worker(config: PipelineConfig, db, vault_path: str, ollama_base_url: str):
    from adapters.outbound import filesystem
    from adapters.outbound.ollama import unload_model

    logger.info("Worker started")
    await db.reset_running()

    while True:
        try:
            processed_any = False

            for stage in config.stages:
                if stage.type not in _HANDLED_TYPES:
                    continue

                jobs = await db.get_pending_jobs(stage.name)
                if not jobs:
                    continue

                logger.info("Stage '%s': processing %d job(s)", stage.name, len(jobs))

                stage_max_concurrent = (
                    stage.max_concurrent if stage.max_concurrent is not None else config.max_concurrent
                )
                sem = asyncio.Semaphore(stage_max_concurrent)

                async def _run(j, _sem=sem):
                    async with _sem:
                        await _process_job(j, stage, db, filesystem, ollama_base_url, config, vault_path)

                await asyncio.gather(*(_run(j) for j in jobs))

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
