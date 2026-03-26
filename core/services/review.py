from __future__ import annotations

import logging
from dataclasses import replace

from core.domain.document import Document, advance, set_done
from core.domain.pipeline import PipelineConfig

logger = logging.getLogger(__name__)


async def approve(doc: Document, config: PipelineConfig, db, now_str: str) -> Document:
    """Advance the document past its current stage to the next."""
    next_stage = config.next_stage(doc.current_stage)
    updated = advance(doc, next_stage.name, now_str) if next_stage else set_done(doc, now_str)
    await db.update(updated)
    await db.append_event(doc.id, doc.current_stage, "approved", now_str)
    logger.info("Approved %s → %s", doc.id[:8], updated.current_stage)
    return updated


async def reject(doc: Document, config: PipelineConfig, db, now_str: str) -> Document:
    """Reset the current stage to pending for re-run."""
    updated = replace(doc, stage_state="pending", updated_at=now_str)
    await db.update(updated)
    await db.append_event(doc.id, doc.current_stage, "rejected", now_str)
    logger.info("Rejected %s → %s:pending", doc.id[:8], doc.current_stage)
    return updated


async def reject_with_clarifications(
    doc: Document,
    clarification_responses: list[dict],
    config: PipelineConfig,
    db,
    now_str: str,
    free_prompt: str = "",
    edited_text: str = "",
) -> Document:
    """Append a Q&A round to qa_history and reset to pending for re-run."""
    stage_name = doc.current_stage
    stage_data = dict(doc.stage_data)
    entry = dict(stage_data.get(stage_name, {}))
    if edited_text:
        entry["clarified_text"] = edited_text
    qa_history = list(entry.get("qa_history", []))
    round_data: dict = {"responses": clarification_responses}
    if free_prompt:
        round_data["free_prompt"] = free_prompt
    qa_history.append(round_data)
    entry["qa_history"] = qa_history
    entry.pop("clarification_requests", None)  # will be regenerated on next run
    stage_data[stage_name] = entry
    updated = replace(doc, stage_state="pending", stage_data=stage_data, updated_at=now_str)
    await db.update(updated)
    await db.append_event(doc.id, stage_name, "clarified", now_str)
    logger.info("Clarify-rerun %s stage=%s round=%d", doc.id[:8], stage_name, len(qa_history))
    return updated
