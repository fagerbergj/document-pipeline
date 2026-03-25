from __future__ import annotations

import logging
from typing import Optional

import httpx

logger = logging.getLogger(__name__)


async def upsert(
    base_url: str,
    collection: str,
    doc_id: str,
    vector: list[float],
    payload: dict,
    api_key: Optional[str] = None,
):
    headers = {"api-key": api_key} if api_key else {}
    async with httpx.AsyncClient(timeout=30.0) as client:
        # Ensure collection exists
        col_resp = await client.get(
            f"{base_url}/collections/{collection}", headers=headers
        )
        if col_resp.status_code == 404:
            create_resp = await client.put(
                f"{base_url}/collections/{collection}",
                headers=headers,
                json={"vectors": {"size": len(vector), "distance": "Cosine"}},
            )
            if create_resp.is_error:
                logger.error("Qdrant create collection error: %s", create_resp.text[:200])
            create_resp.raise_for_status()
            logger.info("Created Qdrant collection '%s'", collection)

        resp = await client.put(
            f"{base_url}/collections/{collection}/points",
            headers=headers,
            json={"points": [{"id": _id_from_uuid(doc_id), "vector": vector, "payload": payload}]},
        )
        if resp.is_error:
            logger.error("Qdrant upsert error: %s", resp.text[:200])
        resp.raise_for_status()
        logger.info("Qdrant upsert ok for %s", doc_id[:8])


async def delete(
    base_url: str,
    collection: str,
    doc_id: str,
    api_key: Optional[str] = None,
):
    headers = {"api-key": api_key} if api_key else {}
    async with httpx.AsyncClient(timeout=30.0) as client:
        resp = await client.post(
            f"{base_url}/collections/{collection}/points/delete",
            headers=headers,
            json={"points": [_id_from_uuid(doc_id)]},
        )
        if resp.is_error:
            logger.error("Qdrant delete error: %s", resp.text[:200])
        resp.raise_for_status()
        logger.info("Qdrant delete ok for %s", doc_id[:8])


def _id_from_uuid(doc_id: str) -> int:
    """Convert UUID string to a stable uint64 for Qdrant point ID."""
    return int(doc_id.replace("-", ""), 16) % (2**63)
