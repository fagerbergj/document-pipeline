from __future__ import annotations

from fastapi import APIRouter, Request
from fastapi.responses import HTMLResponse
from fastapi.templating import Jinja2Templates

router = APIRouter()
templates = Jinja2Templates(directory="ui/templates")

_STATE_ORDER = ["pending", "running", "waiting", "error", "done"]


@router.get("/", response_class=HTMLResponse)
async def dashboard(request: Request):
    db = request.app.state.db
    docs = await db.list_documents()
    counts = await db.status_counts()
    return templates.TemplateResponse(
        "dashboard.html",
        {"request": request, "docs": docs, "counts": counts, "state_order": _STATE_ORDER},
    )


@router.get("/api/documents", response_class=HTMLResponse)
async def documents_table(request: Request):
    """HTMX target: refreshes the document table body."""
    db = request.app.state.db
    docs = await db.list_documents()
    counts = await db.status_counts()
    return templates.TemplateResponse(
        "partials/document_table.html",
        {"request": request, "docs": docs, "counts": counts, "state_order": _STATE_ORDER},
    )


@router.get("/api/documents/{doc_id}/ocr", response_class=HTMLResponse)
async def document_ocr(request: Request, doc_id: str):
    """Returns the OCR text snippet for inline expansion."""
    db = request.app.state.db
    doc = await db.get(doc_id)
    if doc is None:
        return HTMLResponse("<em>Not found</em>", status_code=404)
    ocr_text = (doc.stage_data.get("ocr") or {}).get("ocr_raw", "(no OCR text yet)")
    return templates.TemplateResponse(
        "partials/ocr_detail.html",
        {"request": request, "doc": doc, "ocr_text": ocr_text},
    )


@router.get("/healthz")
async def healthz():
    return {"status": "ok"}
