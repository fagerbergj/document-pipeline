from __future__ import annotations

import hashlib
import logging
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import Optional

from core.domain.document import Document

logger = logging.getLogger(__name__)

_GENERIC_FILENAMES = {"remarkable", "untitled", "image", "attachment", "document"}


def _title_from_meta(meta_json: dict, attachment_filename: Optional[str]) -> Optional[str]:
    for dest in meta_json.get("destinations", []):
        name = str(dest).strip()
        if name:
            return name
    if attachment_filename:
        stem = Path(attachment_filename).stem
        if stem and stem.lower() not in _GENERIC_FILENAMES:
            return stem
    return None


async def ingest(
    image_bytes: bytes,
    meta_json: dict,
    attachment_filename: Optional[str],
    db,
    vault_path: str,
    filesystem,
) -> Optional[Document]:
    content_hash = hashlib.sha256(image_bytes).hexdigest()

    existing = await db.get_by_hash(content_hash)
    if existing:
        logger.info("Duplicate hash %s — skipping", content_hash[:8])
        return None

    now = datetime.now(timezone.utc)
    now_str = now.isoformat()
    date_month = now.strftime("%Y-%m")
    hash_prefix = content_hash[:8]

    # Save source image as artifact
    artifact_id = str(uuid.uuid4())
    filename = f"{hash_prefix}.png"
    filesystem.save_artifact(vault_path, artifact_id, filename, image_bytes)
    png_path = str(Path(vault_path) / "artifacts" / artifact_id / filename)
    logger.info("Saved source artifact: %s", png_path)

    doc = Document(
        id=str(uuid.uuid4()),
        content_hash=content_hash,
        created_at=now_str,
        updated_at=now_str,
        title=_title_from_meta(meta_json, attachment_filename),
        date_month=date_month,
        png_path=png_path,
    )

    await db.insert(doc)
    await db.insert_artifact(doc.id, filename, "image/png", None, now_str, artifact_id=artifact_id)
    # Store ingest metadata in key-value for worker use
    import json as _json
    await db.kv_set(f"ingest_meta:{doc.id}", _json.dumps({
        "meta": meta_json,
        "attachment_filename": attachment_filename,
    }))
    await db.append_event(doc.id, "webhook", "received", now_str)
    logger.info("Created document %s (hash: %s)", doc.id, hash_prefix)
    return doc


_TEXT_TYPES = {"txt", "md"}
_IMAGE_TYPES = {"png", "jpg", "jpeg"}
SUPPORTED_TYPES = _TEXT_TYPES | _IMAGE_TYPES


async def ingest_upload(
    file_bytes: bytes,
    filename: str,
    file_type: str,
    title: Optional[str],
    additional_context: str,
    linked_contexts: list,
    db,
    vault_path: str,
    filesystem,
) -> Optional[Document]:
    """Create a document from a direct upload. Returns None on duplicate."""
    content_hash = hashlib.sha256(file_bytes).hexdigest()

    existing = await db.get_by_hash(content_hash)
    if existing:
        logger.info("Duplicate hash %s — skipping upload", content_hash[:8])
        return None

    now = datetime.now(timezone.utc)
    now_str = now.isoformat()
    date_month = now.strftime("%Y-%m")
    hash_prefix = content_hash[:8]

    png_path = None
    ingest_meta: dict = {"attachment_filename": filename}
    source_artifact_id: Optional[str] = None

    if file_type in _IMAGE_TYPES:
        source_artifact_id = str(uuid.uuid4())
        artifact_filename = f"{hash_prefix}.png"
        filesystem.save_artifact(vault_path, source_artifact_id, artifact_filename, file_bytes)
        png_path = str(Path(vault_path) / "artifacts" / source_artifact_id / artifact_filename)
        logger.info("Saved source artifact: %s", png_path)
    else:
        # Text file — store content for OCR-skip passthrough
        raw_text = file_bytes.decode("utf-8", errors="replace")
        ingest_meta["raw_text"] = raw_text
        ingest_meta["file_type"] = file_type
        if not title:
            stem = Path(filename).stem
            if stem and stem.lower() not in _GENERIC_FILENAMES:
                title = stem
            else:
                for line in raw_text.splitlines():
                    line = line.lstrip("# ").strip()
                    if line and len(line) <= 80:
                        title = line
                        break

    doc = Document(
        id=str(uuid.uuid4()),
        content_hash=content_hash,
        created_at=now_str,
        updated_at=now_str,
        title=title,
        date_month=date_month,
        png_path=png_path,
        additional_context=additional_context or "",
        linked_contexts=linked_contexts or [],
    )

    await db.insert(doc)
    if source_artifact_id:
        await db.insert_artifact(
            doc.id, f"{hash_prefix}.png", "image/png", None, now_str,
            artifact_id=source_artifact_id,
        )
    # Store ingest metadata for worker use
    import json as _json
    await db.kv_set(f"ingest_meta:{doc.id}", _json.dumps(ingest_meta))
    await db.append_event(doc.id, "upload", "received", now_str)
    logger.info("Created document %s via upload (hash: %s)", doc.id, hash_prefix)
    return doc
