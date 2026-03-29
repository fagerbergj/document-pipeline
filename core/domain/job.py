from __future__ import annotations

from dataclasses import dataclass, field


@dataclass
class Job:
    id: str
    document_id: str
    stage: str
    state: str                          # pending | running | waiting | done | error
    config: dict = field(default_factory=dict)   # {"embed_image": bool, ...}
    input_refs: list = field(default_factory=list)   # input artifact refs, e.g. [{"type": "png"}] or [{"stage": "ocr", "field": "ocr_raw"}]
    output_refs: list = field(default_factory=list)  # output artifact refs, e.g. [{"stage": "ocr", "field": "ocr_raw"}] or [{"type": "qdrant", "collection": "..."}]
    llm_log: list = field(default_factory=list)      # raw LLM response strings, one per run attempt
    qa_rounds: list = field(default_factory=list)    # Q&A round history (replaces stage_data[stage]["qa_history"])
    created_at: str = ""
    updated_at: str = ""

    def get_embed_image(self) -> bool:
        return bool(self.config.get("embed_image", False))
