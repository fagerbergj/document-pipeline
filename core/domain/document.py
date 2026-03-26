from __future__ import annotations

from dataclasses import dataclass, field, replace
from typing import Optional


@dataclass
class Document:
    id: str
    content_hash: str
    created_at: str
    updated_at: str
    current_stage: str
    stage_state: str
    title: Optional[str] = None
    date_month: Optional[str] = None
    png_path: Optional[str] = None
    duplicate_of: Optional[str] = None
    stage_data: dict = field(default_factory=dict)
    page_images: list = field(default_factory=list)

    def to_dict(self) -> dict:
        return {
            "id": self.id,
            "content_hash": self.content_hash,
            "created_at": self.created_at,
            "updated_at": self.updated_at,
            "current_stage": self.current_stage,
            "stage_state": self.stage_state,
            "title": self.title,
            "date_month": self.date_month,
            "png_path": self.png_path,
            "duplicate_of": self.duplicate_of,
            "stage_data": self.stage_data,
            "page_images": self.page_images,
        }


def advance(doc: Document, next_stage: str, now: str) -> Document:
    return replace(doc, current_stage=next_stage, stage_state="pending", updated_at=now)


def set_waiting(doc: Document, now: str) -> Document:
    return replace(doc, stage_state="waiting", updated_at=now)


def set_running(doc: Document, now: str) -> Document:
    return replace(doc, stage_state="running", updated_at=now)


def set_error(doc: Document, now: str) -> Document:
    return replace(doc, stage_state="error", updated_at=now)


def set_done(doc: Document, now: str) -> Document:
    return replace(doc, current_stage="done", stage_state="done", updated_at=now)


def set_deleted(doc: Document, now: str) -> Document:
    return replace(doc, current_stage="deleted", stage_state="done", updated_at=now)
