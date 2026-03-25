from __future__ import annotations

import asyncio
import logging
from dataclasses import replace
from datetime import datetime, timezone
from pathlib import Path
from typing import Optional

from core.domain.document import Document, advance, set_done, set_error, set_running, set_waiting
from core.domain.pipeline import PipelineConfig, StageDefinition

logger = logging.getLogger(__name__)

# Stage types implemented in this phase. Worker silently skips others.
_HANDLED_TYPES = {"computer_vision", "manual_review"}

_GENERIC_FILENAMES = {"remarkable", "untitled", "image", "attachment", "document"}


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

    prompt_text = Path(stage.prompt).read_text(encoding="utf-8") if stage.prompt else ""
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

    stage_data = dict(doc.stage_data)
    stage_data[stage.name] = {output_field: ocr_text}

    title = _extract_title(stage_data.get("_ingest", {}), ocr_text)
    safe_title = _sanitize(title)

    try:
        new_png_path = filesystem.rename_png(doc.png_path, safe_title)
    except Exception as exc:
        logger.warning("PNG rename failed: %s", exc)
        new_png_path = doc.png_path

    return stage_data, title, new_png_path


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
            stage_data, title, png_path = await _run_ocr(doc, stage, filesystem, ollama_base_url)
            now_str = datetime.now(timezone.utc).isoformat()
            updated = replace(doc, stage_data=stage_data, title=title, png_path=png_path)
            next_stage = config.next_stage(stage.name)
            updated = advance(updated, next_stage.name, now_str) if next_stage else set_done(updated, now_str)
            await db.update(updated)
            await db.append_event(doc.id, stage.name, "completed", now_str)
            logger.info("Doc %s → %s", doc.id[:8], updated.current_stage)

        elif stage.type == "manual_review":
            await db.update(set_waiting(doc, now_str))
            # No event append here — worker just parks the doc; review service logs the event

    except Exception as exc:
        logger.error("Stage '%s' failed for %s: %s", stage.name, doc.id[:8], exc, exc_info=True)
        now_str = datetime.now(timezone.utc).isoformat()
        await db.append_event(doc.id, stage.name, "failed", now_str, {"error": str(exc)})

        failures = await db.count_failures(doc.id, stage.name)
        if failures < 3:
            backoff = 2 ** failures  # 2s, 4s, 8s
            logger.info("Will retry %s/%s in %ds (attempt %d/3)", doc.id[:8], stage.name, backoff, failures)
            await asyncio.sleep(backoff)
            await db.update(replace(doc, stage_state="pending", updated_at=now_str))
        else:
            logger.error("Doc %s exhausted retries for '%s'", doc.id[:8], stage.name)
            await db.update(set_error(doc, now_str))


async def run_worker(config: PipelineConfig, db, vault_path: str, ollama_base_url: str):
    from adapters.outbound import filesystem
    from adapters.outbound.ollama import unload_model

    sem = asyncio.Semaphore(config.max_concurrent)
    logger.info("Worker started (max_concurrent=%d)", config.max_concurrent)

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
                for doc in docs:
                    async with sem:
                        await _process_document(doc, stage, db, filesystem, ollama_base_url, config)

                if stage.model:
                    await unload_model(ollama_base_url, stage.model)

                processed_any = True

            if not processed_any:
                await asyncio.sleep(5)

        except asyncio.CancelledError:
            logger.info("Worker shutting down")
            raise
        except Exception as exc:
            logger.error("Worker loop error: %s", exc, exc_info=True)
            await asyncio.sleep(5)
