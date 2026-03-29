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

    png_path = filesystem.save_png(vault_path, date_month, hash_prefix, image_bytes)
    logger.info("Saved PNG: %s", png_path)

    doc = Document(
        id=str(uuid.uuid4()),
        content_hash=content_hash,
        created_at=now_str,
        updated_at=now_str,
        current_stage="ocr",
        stage_state="pending",
        title=_title_from_meta(meta_json, attachment_filename),
        date_month=date_month,
        png_path=png_path,
        stage_data={"_ingest": {"meta": meta_json, "attachment_filename": attachment_filename}},
    )

    await db.insert(doc)
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
    document_context: str,
    context_ref: Optional[str],
    db,
    vault_path: str,
    filesystem,
    embed_image: bool = False,
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

    if file_type in _IMAGE_TYPES:
        png_path = filesystem.save_png(vault_path, date_month, hash_prefix, file_bytes)
        logger.info("Saved PNG: %s", png_path)
    else:
        # Text file — store content for OCR-skip passthrough
        raw_text = file_bytes.decode("utf-8", errors="replace")
        ingest_meta["raw_text"] = raw_text
        ingest_meta["file_type"] = file_type
        # Derive title from first non-empty line if not provided
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

    if document_context:
        ingest_meta["document_context"] = document_context

    doc = Document(
        id=str(uuid.uuid4()),
        content_hash=content_hash,
        created_at=now_str,
        updated_at=now_str,
        current_stage="ocr",
        stage_state="pending",
        title=title,
        date_month=date_month,
        png_path=png_path,
        context_ref=context_ref,
        embed_image=embed_image,
        stage_data={"_ingest": ingest_meta},
    )

    await db.insert(doc)
    await db.append_event(doc.id, "upload", "received", now_str)
    logger.info("Created document %s via upload (hash: %s)", doc.id, hash_prefix)
    return doc
