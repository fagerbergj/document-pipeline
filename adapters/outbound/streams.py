"""In-memory per-document token stream queues for SSE delivery."""
from __future__ import annotations

import asyncio

_queues: dict[str, asyncio.Queue] = {}


def get_queue(doc_id: str) -> asyncio.Queue:
    if doc_id not in _queues:
        _queues[doc_id] = asyncio.Queue()
    return _queues[doc_id]


async def put_done(doc_id: str) -> None:
    """Signal end-of-stream to any SSE listener then discard the queue."""
    q = _queues.get(doc_id)
    if q:
        await q.put(None)  # sentinel
    _queues.pop(doc_id, None)
