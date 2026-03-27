from __future__ import annotations

from typing import Any, Optional
from pydantic import BaseModel


# ── Nested ────────────────────────────────────────────────────────────────────

class ClarificationRequest(BaseModel):
    segment: str
    question: str


class StageDisplay(BaseModel):
    name: str
    fields: dict[str, str]


class StageEvent(BaseModel):
    timestamp: str
    stage: str
    event_type: str
    data: Optional[dict[str, Any]] = None


class ReviewDetail(BaseModel):
    stage_name: str
    input_field: Optional[str] = None
    output_field: Optional[str] = None
    input_text: str
    output_text: str
    is_single_output: bool
    confidence: str
    qa_rounds: int
    clarification_requests: list[ClarificationRequest]
    context_updates: str


class ReplayStage(BaseModel):
    name: str


# ── Responses ─────────────────────────────────────────────────────────────────

class DocumentSummary(BaseModel):
    id: str
    title: Optional[str] = None
    current_stage: str
    stage_state: str
    created_at: str
    updated_at: str
    needs_context: bool


class DocumentDetail(BaseModel):
    id: str
    title: Optional[str] = None
    current_stage: str
    stage_state: str
    created_at: str
    updated_at: str
    document_context: str
    context_required: bool
    stage_displays: list[StageDisplay]
    review: Optional[ReviewDetail] = None
    replay_stages: list[ReplayStage]
    needs_context: bool
    events: list[StageEvent]
    has_image: bool


class ContextEntry(BaseModel):
    name: str
    text: str


class Counts(BaseModel):
    pending: Optional[int] = None
    running: Optional[int] = None
    waiting: Optional[int] = None
    error: Optional[int] = None
    done: Optional[int] = None
    by_stage: Optional[dict[str, int]] = None


class StagesResponse(BaseModel):
    stages: list[str]


class OkResponse(BaseModel):
    ok: bool


# ── Request bodies ─────────────────────────────────────────────────────────────

class UpdateTitleBody(BaseModel):
    title: str


class SaveContextBody(BaseModel):
    document_context: str = ""


class ApproveBody(BaseModel):
    edited_text: str = ""


class ClarifyBody(BaseModel):
    answers: dict[str, str] = {}
    free_prompt: str = ""
    edited_text: str = ""


class SaveContextEntryBody(BaseModel):
    name: str
    text: str


class QueryRequest(BaseModel):
    messages: list[dict[str, Any]]
    context: str = ""
    top_k: int = 5
