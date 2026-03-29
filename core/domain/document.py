from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional


@dataclass
class Document:
    id: str
    content_hash: str
    created_at: str
    updated_at: str
    title: Optional[str] = None
    date_month: Optional[str] = None
    png_path: Optional[str] = None
    duplicate_of: Optional[str] = None
    additional_context: str = ""
    linked_contexts: list = field(default_factory=list)  # list of UUID strings

    def to_dict(self) -> dict:
        return {
            "id": self.id,
            "content_hash": self.content_hash,
            "created_at": self.created_at,
            "updated_at": self.updated_at,
            "title": self.title,
            "date_month": self.date_month,
            "png_path": self.png_path,
            "duplicate_of": self.duplicate_of,
            "additional_context": self.additional_context,
            "linked_contexts": self.linked_contexts,
        }
