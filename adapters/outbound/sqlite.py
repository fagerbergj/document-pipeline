from __future__ import annotations

import json
from pathlib import Path
from typing import Optional

import aiosqlite

from core.domain.document import Document

_CREATE_DOCUMENTS = """
CREATE TABLE IF NOT EXISTS documents (
    id TEXT PRIMARY KEY,
    content_hash TEXT UNIQUE NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    current_stage TEXT NOT NULL,
    stage_state TEXT NOT NULL,
    title TEXT,
    date_month TEXT,
    png_path TEXT,
    duplicate_of TEXT REFERENCES documents(id),
    stage_data TEXT NOT NULL DEFAULT '{}'
)
"""

_CREATE_STAGE_EVENTS = """
CREATE TABLE IF NOT EXISTS stage_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    document_id TEXT NOT NULL REFERENCES documents(id),
    timestamp TEXT NOT NULL,
    stage TEXT NOT NULL,
    event_type TEXT NOT NULL,
    data TEXT
)
"""

_CREATE_DOCUMENT_DESTINATIONS = """
CREATE TABLE IF NOT EXISTS document_destinations (
    document_id TEXT NOT NULL REFERENCES documents(id),
    destination_type TEXT NOT NULL,
    external_id TEXT NOT NULL,
    synced_at TEXT NOT NULL,
    PRIMARY KEY (document_id, destination_type)
)
"""

_INDEXES = [
    "CREATE INDEX IF NOT EXISTS idx_documents_stage ON documents(current_stage, stage_state)",
    "CREATE INDEX IF NOT EXISTS idx_documents_hash ON documents(content_hash)",
    "CREATE INDEX IF NOT EXISTS idx_events_document ON stage_events(document_id, stage, event_type)",
]


class Database:
    def __init__(self, path: str):
        self._path = path
        self._conn: Optional[aiosqlite.Connection] = None

    async def init(self):
        Path(self._path).parent.mkdir(parents=True, exist_ok=True)
        self._conn = await aiosqlite.connect(self._path)
        self._conn.row_factory = aiosqlite.Row
        await self._conn.execute("PRAGMA journal_mode=WAL")
        await self._conn.execute(_CREATE_DOCUMENTS)
        await self._conn.execute(_CREATE_STAGE_EVENTS)
        await self._conn.execute(_CREATE_DOCUMENT_DESTINATIONS)
        for idx in _INDEXES:
            await self._conn.execute(idx)
        await self._conn.commit()

    async def close(self):
        if self._conn:
            await self._conn.close()

    def _row_to_doc(self, row) -> Document:
        return Document(
            id=row["id"],
            content_hash=row["content_hash"],
            created_at=row["created_at"],
            updated_at=row["updated_at"],
            current_stage=row["current_stage"],
            stage_state=row["stage_state"],
            title=row["title"],
            date_month=row["date_month"],
            png_path=row["png_path"],
            duplicate_of=row["duplicate_of"],
            stage_data=json.loads(row["stage_data"] or "{}"),
        )

    async def get_by_hash(self, content_hash: str) -> Optional[Document]:
        async with self._conn.execute(
            "SELECT * FROM documents WHERE content_hash = ?", (content_hash,)
        ) as cur:
            row = await cur.fetchone()
            return self._row_to_doc(row) if row else None

    async def insert(self, doc: Document):
        await self._conn.execute(
            """INSERT INTO documents
               (id, content_hash, created_at, updated_at, current_stage, stage_state,
                title, date_month, png_path, duplicate_of, stage_data)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
            (
                doc.id, doc.content_hash, doc.created_at, doc.updated_at,
                doc.current_stage, doc.stage_state, doc.title, doc.date_month,
                doc.png_path, doc.duplicate_of, json.dumps(doc.stage_data),
            ),
        )
        await self._conn.commit()

    async def update(self, doc: Document):
        await self._conn.execute(
            """UPDATE documents SET
               updated_at=?, current_stage=?, stage_state=?,
               title=?, date_month=?, png_path=?, duplicate_of=?, stage_data=?
               WHERE id=?""",
            (
                doc.updated_at, doc.current_stage, doc.stage_state,
                doc.title, doc.date_month, doc.png_path, doc.duplicate_of,
                json.dumps(doc.stage_data), doc.id,
            ),
        )
        await self._conn.commit()

    async def get(self, doc_id: str) -> Optional[Document]:
        async with self._conn.execute(
            "SELECT * FROM documents WHERE id=?", (doc_id,)
        ) as cur:
            row = await cur.fetchone()
            return self._row_to_doc(row) if row else None

    async def get_pending(self, stage_name: str) -> list[Document]:
        async with self._conn.execute(
            "SELECT * FROM documents WHERE current_stage=? AND stage_state='pending' ORDER BY created_at ASC",
            (stage_name,),
        ) as cur:
            return [self._row_to_doc(r) for r in await cur.fetchall()]

    async def get_waiting(self) -> list[Document]:
        async with self._conn.execute(
            "SELECT * FROM documents WHERE stage_state='waiting'"
            " AND current_stage NOT IN ('deleted')"
            " ORDER BY created_at ASC"
        ) as cur:
            return [self._row_to_doc(r) for r in await cur.fetchall()]

    async def list_documents(
        self,
        stage: Optional[str] = None,
        state: Optional[str] = None,
        sort: str = "created_desc",
    ) -> list[Document]:
        conditions = ["current_stage != 'deleted'"]
        params: list = []
        if stage:
            conditions.append("current_stage = ?")
            params.append(stage)
        if state:
            conditions.append("stage_state = ?")
            params.append(state)
        order = {
            "created_desc": "created_at DESC",
            "created_asc": "created_at ASC",
            "title_asc": "LOWER(COALESCE(title,'')) ASC",
            "title_desc": "LOWER(COALESCE(title,'')) DESC",
        }.get(sort, "created_at DESC")
        sql = f"SELECT * FROM documents WHERE {' AND '.join(conditions)} ORDER BY {order}"
        async with self._conn.execute(sql, params) as cur:
            return [self._row_to_doc(r) for r in await cur.fetchall()]

    async def append_event(
        self, document_id: str, stage: str, event_type: str, timestamp: str,
        data: Optional[dict] = None,
    ):
        await self._conn.execute(
            "INSERT INTO stage_events (document_id, timestamp, stage, event_type, data) VALUES (?, ?, ?, ?, ?)",
            (document_id, timestamp, stage, event_type, json.dumps(data) if data else None),
        )
        await self._conn.commit()

    async def count_failures(self, document_id: str, stage: str) -> int:
        async with self._conn.execute(
            "SELECT COUNT(*) FROM stage_events WHERE document_id=? AND stage=? AND event_type='failed'",
            (document_id, stage),
        ) as cur:
            row = await cur.fetchone()
            return row[0] if row else 0

    async def status_counts(self) -> dict:
        async with self._conn.execute(
            """SELECT stage_state, COUNT(*) as cnt FROM documents
               WHERE current_stage NOT IN ('deleted') GROUP BY stage_state"""
        ) as cur:
            return {row["stage_state"]: row["cnt"] for row in await cur.fetchall()}
