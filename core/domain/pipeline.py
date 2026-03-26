from __future__ import annotations

import os
import re
from dataclasses import dataclass
from typing import Optional

import yaml


def _expand(value):
    if isinstance(value, str):
        return re.sub(
            r"\$\{(\w+)\}", lambda m: os.environ.get(m.group(1), m.group(0)), value
        )
    if isinstance(value, dict):
        return {k: _expand(v) for k, v in value.items()}
    if isinstance(value, list):
        return [_expand(item) for item in value]
    return value


@dataclass
class StageDefinition:
    name: str
    type: str
    model: Optional[str] = None
    prompt: Optional[str] = None
    input: Optional[str] = None
    output: Optional[str] = None
    outputs: Optional[list] = None
    require_context: bool = False
    destinations: Optional[list] = None
    metadata_fields: Optional[list] = None
    start_if: Optional[dict] = None
    continue_if: Optional[list] = None
    max_concurrent: Optional[int] = None
    vision: bool = False


@dataclass
class PipelineConfig:
    max_concurrent: int
    stages: list[StageDefinition]

    @classmethod
    def from_yaml(cls, path: str) -> PipelineConfig:
        with open(path) as f:
            raw = yaml.safe_load(f)
        raw = _expand(raw)
        stages = [
            StageDefinition(
                name=s["name"],
                type=s["type"],
                model=s.get("model"),
                prompt=s.get("prompt"),
                input=s.get("input"),
                output=s.get("output"),
                outputs=s.get("outputs"),
                require_context=s.get("require_context", False),
                destinations=s.get("destinations"),
                metadata_fields=s.get("metadata_fields"),
                start_if=s.get("start_if"),
                continue_if=s.get("continue_if"),
                max_concurrent=s.get("max_concurrent"),
                vision=s.get("vision", False),
            )
            for s in raw.get("stages", [])
        ]
        return cls(max_concurrent=raw.get("max_concurrent", 1), stages=stages)

    def next_stage(self, current_name: str) -> Optional[StageDefinition]:
        for i, s in enumerate(self.stages):
            if s.name == current_name and i + 1 < len(self.stages):
                return self.stages[i + 1]
        return None

    def prev_stage(self, current_name: str) -> Optional[StageDefinition]:
        for i, s in enumerate(self.stages):
            if s.name == current_name and i > 0:
                return self.stages[i - 1]
        return None

    def get_stage(self, name: str) -> Optional[StageDefinition]:
        return next((s for s in self.stages if s.name == name), None)
