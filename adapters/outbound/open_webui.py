from __future__ import annotations

import json
import logging
from typing import Optional

import httpx

logger = logging.getLogger(__name__)


def _build_markdown(title: str, text: str, metadata: Optional[dict]) -> str:
    """Render the document as markdown with YAML frontmatter."""
    meta = metadata or {}
    lines = ["---"]
    lines.append(f"title: {json.dumps(title)}")
    for key, value in meta.items():
        if isinstance(value, list):
            lines.append(f"{key}: {json.dumps(value)}")
        elif value is not None:
            lines.append(f"{key}: {json.dumps(str(value))}")
    lines.append("---")
    lines.append("")
    lines.append(f"# {title}")
    lines.append("")
    lines.append(text)
    return "\n".join(lines)


async def upsert(
    base_url: str,
    api_key: str,
    knowledge_id: str,
    doc_id: str,
    title: str,
    text: str,
    metadata: Optional[dict] = None,
):
    """Upload document as markdown with frontmatter to Open WebUI and add to a knowledge base.

    If a file with the same doc_id already exists (tracked via filename), it is
    deleted first so the knowledge base stays up-to-date on re-runs.
    """
    headers = {"Authorization": f"Bearer {api_key}"}
    filename = f"{doc_id}.md"
    content = _build_markdown(title, text, metadata)

    async with httpx.AsyncClient(timeout=60.0, base_url=base_url) as client:
        # Delete existing file for this doc if present (check both .md and legacy .txt)
        for name in (filename, f"{doc_id}.txt"):
            existing = await _find_file(client, headers, name)
            if existing:
                await _delete_file(client, headers, knowledge_id, existing["id"])

        # Upload the markdown file
        file_resp = await client.post(
            "/api/v1/files/",
            headers=headers,
            files={"file": (filename, content.encode(), "text/markdown")},
            data={"metadata": "{}"},
        )
        if file_resp.is_error:
            logger.error("Open WebUI file upload error %s: %s", file_resp.status_code, file_resp.text[:200])
        file_resp.raise_for_status()
        file_id = file_resp.json()["id"]
        logger.info("Uploaded file %s to Open WebUI (file_id=%s)", filename, file_id)

        # Add to knowledge base
        kb_resp = await client.post(
            f"/api/v1/knowledge/{knowledge_id}/file/add",
            headers={**headers, "Content-Type": "application/json"},
            json={"file_id": file_id},
        )
        if kb_resp.is_error:
            logger.error("Open WebUI knowledge add error %s: %s", kb_resp.status_code, kb_resp.text[:200])
        kb_resp.raise_for_status()
        logger.info("Added %s to knowledge base %s", doc_id[:8], knowledge_id)


async def delete(
    base_url: str,
    api_key: str,
    knowledge_id: str,
    doc_id: str,
):
    headers = {"Authorization": f"Bearer {api_key}"}
    async with httpx.AsyncClient(timeout=30.0, base_url=base_url) as client:
        for filename in (f"{doc_id}.md", f"{doc_id}.txt"):
            existing = await _find_file(client, headers, filename)
            if existing:
                await _delete_file(client, headers, knowledge_id, existing["id"])


async def _find_file(client: httpx.AsyncClient, headers: dict, filename: str) -> Optional[dict]:
    resp = await client.get("/api/v1/files/", headers=headers)
    if resp.is_error:
        return None
    for f in resp.json():
        if f.get("filename") == filename:
            return f
    return None


async def _delete_file(client: httpx.AsyncClient, headers: dict, knowledge_id: str, file_id: str):
    # Remove from knowledge base first
    await client.post(
        f"/api/v1/knowledge/{knowledge_id}/file/remove",
        headers={**headers, "Content-Type": "application/json"},
        json={"file_id": file_id},
    )
    # Then delete the file itself
    await client.delete(f"/api/v1/files/{file_id}", headers=headers)
    logger.info("Deleted file %s from Open WebUI", file_id)
