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

    if image_bytes[:4] == b"%PDF":
        filesystem.save_pdf(vault_path, date_month, hash_prefix, image_bytes)
        pdf_path = str(Path(vault_path) / date_month / f"{hash_prefix}.pdf")
        page_images = filesystem.pdf_to_page_images(pdf_path, vault_path, date_month, hash_prefix)
        png_path = page_images[0] if page_images else None
        logger.info("Saved PDF and %d page image(s): %s", len(page_images), pdf_path)
    else:
        png_path = filesystem.save_png(vault_path, date_month, hash_prefix, image_bytes)
        page_images = [png_path]
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
        page_images=page_images,
        stage_data={"_ingest": {"meta": meta_json, "attachment_filename": attachment_filename}},
    )

    await db.insert(doc)
    await db.append_event(doc.id, "webhook", "received", now_str)
    logger.info("Created document %s (hash: %s)", doc.id, hash_prefix)
    return doc
