from __future__ import annotations

from dataclasses import dataclass, field


@dataclass
class Job:
    id: str
    document_id: str
    stage: str
    status: str                           # pending | running | waiting | done | error
    options: dict = field(default_factory=dict)   # {require_context: bool, embed: {embed_image: bool}}
    runs: list = field(default_factory=list)      # list of Run dicts, appended on each execution
    created_at: str = ""
    updated_at: str = ""

    def get_embed_image(self) -> bool:
        return bool(self.options.get("embed", {}).get("embed_image", False))

    def get_require_context(self) -> bool:
        return bool(self.options.get("require_context", False))
