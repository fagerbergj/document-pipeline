from __future__ import annotations

import logging
from dataclasses import replace as dc_replace

from core.domain.document import Document, advance, set_done
from core.domain.pipeline import PipelineConfig

logger = logging.getLogger(__name__)


async def approve(doc: Document, config: PipelineConfig, db, now_str: str) -> Document:
    """Advance the document past its current stage to the next."""
    stage_name = doc.current_stage
    next_stage = config.next_stage(stage_name)
    updated = advance(doc, next_stage.name, now_str) if next_stage else set_done(doc, now_str)
    await db.update(updated)
    # Mark current job done and create next job row
    job = await db.get_job(doc.id, stage_name)
    if job is not None:
        await db.upsert_job(dc_replace(job, state="done", updated_at=now_str))
    if next_stage:
        from core.domain.job import Job
        import uuid as _uuid
        next_job = Job(
            id=str(_uuid.uuid4()),
            document_id=doc.id,
            stage=next_stage.name,
            state="pending",
            created_at=now_str,
            updated_at=now_str,
        )
        await db.upsert_job(next_job)
    await db.append_event(doc.id, stage_name, "approved", now_str)
    logger.info("Approved %s → %s", doc.id[:8], updated.current_stage)
    return updated


async def reject(doc: Document, config: PipelineConfig, db, now_str: str) -> Document:
    """Reset the current stage to pending for re-run."""
    updated = dc_replace(doc, stage_state="pending", updated_at=now_str)
    await db.update(updated)
    job = await db.get_job(doc.id, doc.current_stage)
    if job is not None:
        await db.upsert_job(dc_replace(job, state="pending", updated_at=now_str))
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
    """Append a Q&A round to the job's qa_rounds and reset to pending for re-run."""
    stage_name = doc.current_stage
    stage_data = dict(doc.stage_data)
    entry = dict(stage_data.get(stage_name, {}))
    if edited_text:
        entry["clarified_text"] = edited_text
    if free_prompt:
        entry["free_prompt"] = free_prompt  # kept in stage_data for prompt template rendering
    entry.pop("clarification_requests", None)  # will be regenerated on next run
    stage_data[stage_name] = entry

    round_data: dict = {"responses": clarification_responses}
    if free_prompt:
        round_data["free_prompt"] = free_prompt

    # Write qa_rounds to the job row (authoritative), falling back to stage_data for old docs
    job = await db.get_job(doc.id, stage_name)
    if job is not None:
        new_qa = list(job.qa_rounds) + [round_data]
        await db.upsert_job(dc_replace(job, qa_rounds=new_qa, state="pending", updated_at=now_str))
        qa_count = len(new_qa)
    else:
        # Fallback for pre-migration documents without a job row
        qa_history = list(entry.get("qa_history", []))
        qa_history.append(round_data)
        entry["qa_history"] = qa_history
        stage_data[stage_name] = entry
        qa_count = len(qa_history)

    updated = dc_replace(doc, stage_state="pending", stage_data=stage_data, updated_at=now_str)
    await db.update(updated)
    await db.append_event(doc.id, stage_name, "clarified", now_str)
    logger.info("Clarify-rerun %s stage=%s round=%d", doc.id[:8], stage_name, qa_count)
    return updated
