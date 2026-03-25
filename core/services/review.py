from __future__ import annotations

import logging
from dataclasses import replace

from core.domain.document import Document, advance, set_done
from core.domain.pipeline import PipelineConfig

logger = logging.getLogger(__name__)


async def approve(doc: Document, config: PipelineConfig, db, now_str: str) -> Document:
    next_stage = config.next_stage(doc.current_stage)
    updated = advance(doc, next_stage.name, now_str) if next_stage else set_done(doc, now_str)
    await db.update(updated)
    await db.append_event(doc.id, doc.current_stage, "reviewed", now_str)
    logger.info("Approved %s → %s", doc.id[:8], updated.current_stage)
    return updated


async def reject(doc: Document, config: PipelineConfig, db, now_str: str) -> Document:
    prev = config.prev_stage(doc.current_stage)
    if prev is None:
        raise ValueError(f"No previous stage for '{doc.current_stage}'")
    updated = advance(doc, prev.name, now_str)
    await db.update(updated)
    await db.append_event(doc.id, doc.current_stage, "rejected", now_str)
    logger.info("Rejected %s → %s", doc.id[:8], updated.current_stage)
    return updated


async def reject_with_clarifications(
    doc: Document,
    stage_name: str,
    clarification_responses: list[dict],
    config: PipelineConfig,
    db,
    now_str: str,
) -> Document:
    """Save reviewer answers into stage_data[stage_name] and reset that stage to pending."""
    stage_data = dict(doc.stage_data)
    entry = dict(stage_data.get(stage_name, {}))
    entry["clarification_responses"] = clarification_responses
    stage_data[stage_name] = entry
    updated = replace(
        doc,
        current_stage=stage_name,
        stage_state="pending",
        stage_data=stage_data,
        updated_at=now_str,
    )
    await db.update(updated)
    await db.append_event(doc.id, doc.current_stage, "rejected", now_str)
    logger.info("Clarify-rejected %s → %s:pending", doc.id[:8], stage_name)
    return updated
