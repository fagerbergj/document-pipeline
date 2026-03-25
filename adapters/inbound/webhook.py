from __future__ import annotations

import json
import logging

from fastapi import APIRouter, HTTPException, Request

from adapters.outbound import filesystem
from core.services.ingest import ingest

logger = logging.getLogger(__name__)
router = APIRouter()


@router.post("/webhook")
async def webhook(request: Request):
    """
    Receives a document send from the reMarkable tablet via rmfakecloud.
    multipart/form-data with:
      - data:       JSON string with document metadata
      - attachment: rendered PNG of the current sheet
    Returns immediately; OCR runs asynchronously.
    """
    if "multipart/form-data" not in request.headers.get("content-type", ""):
        raise HTTPException(status_code=415, detail="Expected multipart/form-data")

    form = await request.form()

    try:
        meta_json = json.loads(str(form.get("data") or "{}"))
    except json.JSONDecodeError:
        meta_json = {}

    # Find the image attachment — try known field name first, then any file field
    attachment = form.get("attachment")
    attachment_filename: str | None = None
    image_bytes: bytes | None = None

    if attachment and hasattr(attachment, "read"):
        attachment_filename = getattr(attachment, "filename", None)
        image_bytes = await attachment.read()
    else:
        for key in form:
            field = form[key]
            if hasattr(field, "read"):
                raw = await field.read()
                if raw:
                    attachment_filename = getattr(field, "filename", None)
                    image_bytes = raw
                    break

    if not image_bytes:
        raise HTTPException(status_code=422, detail="No image attachment found")

    logger.info("Webhook: %d bytes (filename: %s)", len(image_bytes), attachment_filename)

    db = request.app.state.db
    vault_path = request.app.state.vault_path

    doc = await ingest(image_bytes, meta_json, attachment_filename, db, vault_path, filesystem)
    if doc is None:
        return {"status": "duplicate"}

    return {"status": "ok", "id": doc.id}
