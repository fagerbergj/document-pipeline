from __future__ import annotations

import logging
from typing import Optional

import httpx

logger = logging.getLogger(__name__)


async def _collection_uses_named_vectors(
    client: httpx.AsyncClient,
    base_url: str,
    collection: str,
    headers: dict,
) -> bool:
    """Return True if the collection was created with named vectors (text/image)."""
    resp = await client.get(f"{base_url}/collections/{collection}", headers=headers)
    if resp.is_error:
        return False
    try:
        vectors_cfg = resp.json()["result"]["config"]["params"]["vectors"]
        # Named: {"text": {"size": N, ...}, "image": {...}}
        # Unnamed: {"size": N, "distance": "Cosine"}
        return "size" not in vectors_cfg
    except Exception:
        return False


async def upsert(
    base_url: str,
    collection: str,
    doc_id: str,
    vector: list[float],
    payload: dict,
    api_key: Optional[str] = None,
    image_vector: Optional[list[float]] = None,
):
    headers = {"api-key": api_key} if api_key else {}
    async with httpx.AsyncClient(timeout=30.0) as client:
        # Ensure collection exists
        col_resp = await client.get(
            f"{base_url}/collections/{collection}", headers=headers
        )
        if col_resp.status_code == 404:
            if image_vector is not None:
                vectors_config = {
                    "text": {"size": len(vector), "distance": "Cosine"},
                    "image": {"size": len(image_vector), "distance": "Cosine"},
                }
            else:
                vectors_config = {"size": len(vector), "distance": "Cosine"}
            create_resp = await client.put(
                f"{base_url}/collections/{collection}",
                headers=headers,
                json={"vectors": vectors_config},
            )
            if create_resp.is_error:
                logger.error("Qdrant create collection error: %s", create_resp.text[:200])
            create_resp.raise_for_status()
            logger.info("Created Qdrant collection '%s'", collection)
            named = image_vector is not None
        else:
            named = await _collection_uses_named_vectors(client, base_url, collection, headers)
            if named and image_vector is None:
                logger.debug("Named-vector collection '%s'; upserting text vector only", collection)
            elif not named and image_vector is not None:
                logger.warning(
                    "embed_image=true but collection '%s' uses unnamed vectors — "
                    "image vector skipped. Create a new collection to enable image search.",
                    collection,
                )

        if named:
            point_vector: dict | list = {"text": vector}
            if image_vector is not None:
                point_vector["image"] = image_vector
        else:
            point_vector = vector

        resp = await client.put(
            f"{base_url}/collections/{collection}/points",
            headers=headers,
            json={"points": [{"id": _id_from_uuid(doc_id), "vector": point_vector, "payload": payload}]},
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


async def search(
    base_url: str,
    collection: str,
    vector: list[float],
    top_k: int = 5,
    api_key: Optional[str] = None,
) -> list[dict]:
    """Return top-k results from Qdrant as list of payload dicts with added 'score' key."""
    headers = {"api-key": api_key} if api_key else {}
    async with httpx.AsyncClient(timeout=30.0) as client:
        col_resp = await client.get(
            f"{base_url}/collections/{collection}", headers=headers
        )
        if col_resp.status_code == 404:
            return []

        # Auto-detect named vs unnamed and build search vector accordingly
        named = await _collection_uses_named_vectors(client, base_url, collection, headers)
        search_vector = {"name": "text", "vector": vector} if named else vector

        resp = await client.post(
            f"{base_url}/collections/{collection}/points/search",
            headers=headers,
            json={"vector": search_vector, "limit": top_k, "with_payload": True},
        )
        if resp.is_error:
            logger.error("Qdrant search error: %s", resp.text[:200])
            return []
        hits = resp.json().get("result", [])
        results = []
        for h in hits:
            entry = dict(h.get("payload") or {})
            entry["score"] = h.get("score", 0.0)
            results.append(entry)
        return results


def _id_from_uuid(doc_id: str) -> int:
    """Convert UUID string to a stable uint64 for Qdrant point ID."""
    return int(doc_id.replace("-", ""), 16) % (2**63)
