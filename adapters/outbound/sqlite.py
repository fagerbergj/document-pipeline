from __future__ import annotations

import json
import uuid
from pathlib import Path
from typing import Optional

import aiosqlite

from core.domain.document import Document
from core.domain.job import Job

_CREATE_CONTEXTS = """
CREATE TABLE IF NOT EXISTS contexts (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    text TEXT NOT NULL,
    created_at TEXT NOT NULL
)
"""

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
    context_ref TEXT REFERENCES contexts(id) ON DELETE SET NULL,
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

_CREATE_KEY_VALUE = """
CREATE TABLE IF NOT EXISTS key_value (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
)
"""

_CREATE_CHAT_SESSIONS = """
CREATE TABLE IF NOT EXISTS chat_sessions (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    context TEXT NOT NULL DEFAULT '',
    top_k INTEGER NOT NULL DEFAULT 5,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)
"""

_CREATE_CHAT_MESSAGES = """
CREATE TABLE IF NOT EXISTS chat_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    sources TEXT,
    created_at TEXT NOT NULL
)
"""

_CREATE_JOBS = """
CREATE TABLE IF NOT EXISTS jobs (
    id          TEXT PRIMARY KEY,
    document_id TEXT NOT NULL REFERENCES documents(id),
    stage       TEXT NOT NULL,
    state       TEXT NOT NULL DEFAULT 'pending',
    config      TEXT NOT NULL DEFAULT '{}',
    input_refs  TEXT NOT NULL DEFAULT '[]',
    output_refs TEXT NOT NULL DEFAULT '[]',
    llm_log     TEXT NOT NULL DEFAULT '[]',
    qa_rounds   TEXT NOT NULL DEFAULT '[]',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE(document_id, stage)
)
"""

_INDEXES = [
    "CREATE INDEX IF NOT EXISTS idx_documents_stage ON documents(current_stage, stage_state)",
    "CREATE INDEX IF NOT EXISTS idx_documents_hash ON documents(content_hash)",
    "CREATE INDEX IF NOT EXISTS idx_events_document ON stage_events(document_id, stage, event_type)",
    "CREATE INDEX IF NOT EXISTS idx_chat_messages_session ON chat_messages(session_id, id ASC)",
    "CREATE INDEX IF NOT EXISTS idx_jobs_document ON jobs(document_id, stage)",
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
        await self._conn.execute("PRAGMA foreign_keys=ON")
        await self._conn.execute(_CREATE_CONTEXTS)
        await self._conn.execute(_CREATE_DOCUMENTS)
        await self._conn.execute(_CREATE_STAGE_EVENTS)
        await self._conn.execute(_CREATE_DOCUMENT_DESTINATIONS)
        await self._conn.execute(_CREATE_KEY_VALUE)
        await self._conn.execute(_CREATE_CHAT_SESSIONS)
        await self._conn.execute(_CREATE_CHAT_MESSAGES)
        await self._conn.execute(_CREATE_JOBS)
        for idx in _INDEXES:
            await self._conn.execute(idx)
        # Migration: add context_ref column if this is an existing DB
        try:
            await self._conn.execute(
                "ALTER TABLE documents ADD COLUMN"
                " context_ref TEXT REFERENCES contexts(id) ON DELETE SET NULL"
            )
        except Exception:
            pass  # column already exists
        # Migration: add embed_image column if this is an existing DB
        try:
            await self._conn.execute(
                "ALTER TABLE documents ADD COLUMN embed_image INTEGER NOT NULL DEFAULT 0"
            )
        except Exception:
            pass  # column already exists
        await self._conn.commit()
        # Backfill jobs table from existing documents (idempotent — INSERT OR IGNORE)
        await self._backfill_jobs()

    async def close(self):
        if self._conn:
            await self._conn.close()

    def _row_to_doc(self, row) -> Document:
        d = dict(row)
        return Document(
            id=d["id"],
            content_hash=d["content_hash"],
            created_at=d["created_at"],
            updated_at=d["updated_at"],
            current_stage=d["current_stage"],
            stage_state=d["stage_state"],
            title=d["title"],
            date_month=d["date_month"],
            png_path=d["png_path"],
            duplicate_of=d["duplicate_of"],
            context_ref=d.get("context_ref"),
            embed_image=bool(d.get("embed_image", 0)),
            stage_data=json.loads(d["stage_data"] or "{}"),
        )

    def _row_to_job(self, row) -> Job:
        d = dict(row)
        return Job(
            id=d["id"],
            document_id=d["document_id"],
            stage=d["stage"],
            state=d["state"],
            config=json.loads(d["config"] or "{}"),
            input_refs=json.loads(d.get("input_refs") or "[]"),
            output_refs=json.loads(d.get("output_refs") or "[]"),
            llm_log=json.loads(d["llm_log"] or "[]"),
            qa_rounds=json.loads(d["qa_rounds"] or "[]"),
            created_at=d["created_at"],
            updated_at=d["updated_at"],
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
                title, date_month, png_path, duplicate_of, context_ref, embed_image, stage_data)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
            (
                doc.id, doc.content_hash, doc.created_at, doc.updated_at,
                doc.current_stage, doc.stage_state, doc.title, doc.date_month,
                doc.png_path, doc.duplicate_of, doc.context_ref,
                1 if doc.embed_image else 0,
                json.dumps(doc.stage_data),
            ),
        )
        await self._conn.commit()

    async def update(self, doc: Document):
        await self._conn.execute(
            """UPDATE documents SET
               updated_at=?, current_stage=?, stage_state=?,
               title=?, date_month=?, png_path=?, duplicate_of=?, context_ref=?,
               embed_image=?, stage_data=?
               WHERE id=?""",
            (
                doc.updated_at, doc.current_stage, doc.stage_state,
                doc.title, doc.date_month, doc.png_path, doc.duplicate_of,
                doc.context_ref, 1 if doc.embed_image else 0,
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

    async def reset_running(self) -> int:
        """On startup, reset any docs/jobs stuck in 'running' back to 'pending'."""
        async with self._conn.execute(
            "UPDATE documents SET stage_state='pending' WHERE stage_state='running'"
        ) as cur:
            count = cur.rowcount
        await self._conn.execute(
            "UPDATE jobs SET state='pending' WHERE state='running'"
        )
        await self._conn.commit()
        return count

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
        stages: Optional[list] = None,
        states: Optional[list] = None,
        sort: str = "created_asc",
    ) -> list[Document]:
        conditions = ["current_stage != 'deleted'"]
        params: list = []
        # multi-value takes precedence over single-value
        active_stages = stages or ([stage] if stage else None)
        active_states = states or ([state] if state else None)
        if active_stages:
            placeholders = ",".join("?" * len(active_stages))
            conditions.append(f"current_stage IN ({placeholders})")
            params.extend(active_stages)
        if active_states:
            placeholders = ",".join("?" * len(active_states))
            conditions.append(f"stage_state IN ({placeholders})")
            params.extend(active_states)
        order = {
            "pipeline": "current_stage ASC, created_at ASC",
            "created_desc": "created_at DESC",
            "created_asc": "created_at ASC",
            "title_asc": "LOWER(COALESCE(title,'')) ASC",
            "title_desc": "LOWER(COALESCE(title,'')) DESC",
        }.get(sort, "current_stage ASC, created_at ASC")
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

    async def get_events(self, document_id: str) -> list[dict]:
        async with self._conn.execute(
            "SELECT timestamp, stage, event_type, data FROM stage_events WHERE document_id=? ORDER BY id ASC",
            (document_id,),
        ) as cur:
            rows = await cur.fetchall()
            return [
                {
                    "timestamp": r["timestamp"],
                    "stage": r["stage"],
                    "event_type": r["event_type"],
                    "data": json.loads(r["data"]) if r["data"] else None,
                }
                for r in rows
            ]

    async def delete(self, document_id: str) -> None:
        await self._conn.execute("DELETE FROM stage_events WHERE document_id=?", (document_id,))
        await self._conn.execute("DELETE FROM document_destinations WHERE document_id=?", (document_id,))
        await self._conn.execute("DELETE FROM jobs WHERE document_id=?", (document_id,))
        await self._conn.execute("DELETE FROM documents WHERE id=?", (document_id,))
        await self._conn.commit()

    async def clear_errors(self, document_id: str) -> None:
        await self._conn.execute(
            "DELETE FROM stage_events WHERE document_id=? AND event_type='failed'",
            (document_id,),
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
            state_counts = {row["stage_state"]: row["cnt"] for row in await cur.fetchall()}
        async with self._conn.execute(
            """SELECT current_stage, COUNT(*) as cnt FROM documents
               WHERE current_stage NOT IN ('deleted') GROUP BY current_stage"""
        ) as cur:
            stage_counts = {row["current_stage"]: row["cnt"] for row in await cur.fetchall()}
        return {**state_counts, "by_stage": stage_counts}

    # ── Jobs ──────────────────────────────────────────────────────────────────

    async def upsert_job(self, job: Job) -> None:
        await self._conn.execute(
            """INSERT INTO jobs
               (id, document_id, stage, state, config, input_refs, output_refs,
                llm_log, qa_rounds, created_at, updated_at)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
               ON CONFLICT(document_id, stage) DO UPDATE SET
                 state=excluded.state,
                 config=excluded.config,
                 input_refs=excluded.input_refs,
                 output_refs=excluded.output_refs,
                 llm_log=excluded.llm_log,
                 qa_rounds=excluded.qa_rounds,
                 updated_at=excluded.updated_at""",
            (
                job.id, job.document_id, job.stage, job.state,
                json.dumps(job.config),
                json.dumps(job.input_refs),
                json.dumps(job.output_refs),
                json.dumps(job.llm_log),
                json.dumps(job.qa_rounds),
                job.created_at, job.updated_at,
            ),
        )
        await self._conn.commit()

    async def get_job(self, document_id: str, stage: str) -> Optional[Job]:
        async with self._conn.execute(
            "SELECT * FROM jobs WHERE document_id=? AND stage=?", (document_id, stage)
        ) as cur:
            row = await cur.fetchone()
            return self._row_to_job(row) if row else None

    async def get_job_by_id(self, job_id: str) -> Optional[Job]:
        async with self._conn.execute(
            "SELECT * FROM jobs WHERE id=?", (job_id,)
        ) as cur:
            row = await cur.fetchone()
            return self._row_to_job(row) if row else None

    async def update_job_config(self, job_id: str, config: dict, now: str) -> Optional[Job]:
        await self._conn.execute(
            "UPDATE jobs SET config=?, updated_at=? WHERE id=?",
            (json.dumps(config), now, job_id),
        )
        await self._conn.commit()
        return await self.get_job_by_id(job_id)

    async def list_jobs_for_document(self, document_id: str) -> list[Job]:
        async with self._conn.execute(
            "SELECT * FROM jobs WHERE document_id=? ORDER BY created_at ASC",
            (document_id,),
        ) as cur:
            return [self._row_to_job(r) for r in await cur.fetchall()]

    async def get_events_for_job(
        self,
        document_id: str,
        stage: str,
        page_size: int = 100,
        after_id: Optional[int] = None,
    ) -> tuple[list[dict], Optional[int]]:
        """Return paginated events for a specific (document_id, stage) pair."""
        params: list = [document_id, stage]
        where_extra = ""
        if after_id is not None:
            where_extra = " AND id > ?"
            params.append(after_id)
        async with self._conn.execute(
            f"SELECT id, timestamp, stage, event_type, data FROM stage_events"
            f" WHERE document_id=? AND stage=?{where_extra} ORDER BY id ASC LIMIT ?",
            params + [page_size + 1],
        ) as cur:
            rows = await cur.fetchall()
        events = [
            {
                "id": r["id"],
                "timestamp": r["timestamp"],
                "stage": r["stage"],
                "event_type": r["event_type"],
                "data": json.loads(r["data"]) if r["data"] else None,
            }
            for r in rows
        ]
        has_more = len(events) > page_size
        if has_more:
            events = events[:page_size]
        next_after = events[-1]["id"] if has_more and events else None
        return events, next_after

    async def _backfill_jobs(self) -> None:
        """Create job rows for existing documents that predate the jobs table (idempotent)."""
        import datetime as _dt
        _EXCLUDE = {
            "qa_history", "clarification_requests", "confidence",
            "document_context_update", "linked_context_update", "free_prompt",
        }
        async with self._conn.execute("SELECT * FROM documents") as cur:
            rows = await cur.fetchall()
        now = _dt.datetime.utcnow().isoformat() + "Z"
        for row in rows:
            doc = self._row_to_doc(row)
            if doc.current_stage in ("done", "deleted"):
                # Create done job rows for all stages in stage_data
                for stage_name, sdata in doc.stage_data.items():
                    if stage_name == "_ingest" or not isinstance(sdata, dict):
                        continue
                    qa_rounds = sdata.get("qa_history", [])
                    output = {k: v for k, v in sdata.items() if k not in _EXCLUDE}
                    output_refs = [{"stage": stage_name, "fields": list(output.keys())}] if output else []
                    await self._conn.execute(
                        """INSERT OR IGNORE INTO jobs
                           (id, document_id, stage, state, config, input_refs, output_refs,
                            llm_log, qa_rounds, created_at, updated_at)
                           VALUES (?, ?, ?, 'done', '{}', '[]', ?, '[]', ?, ?, ?)""",
                        (
                            str(uuid.uuid4()), doc.id, stage_name,
                            json.dumps(output_refs),
                            json.dumps(qa_rounds),
                            doc.created_at, now,
                        ),
                    )
            else:
                # Create done rows for completed stages (those with data but not current)
                for stage_name, sdata in doc.stage_data.items():
                    if stage_name == "_ingest" or not isinstance(sdata, dict):
                        continue
                    is_current = (stage_name == doc.current_stage)
                    state = doc.stage_state if is_current else "done"
                    qa_rounds = sdata.get("qa_history", [])
                    output = {k: v for k, v in sdata.items() if k not in _EXCLUDE}
                    output_refs = [{"stage": stage_name, "fields": list(output.keys())}] if output else []
                    await self._conn.execute(
                        """INSERT OR IGNORE INTO jobs
                           (id, document_id, stage, state, config, input_refs, output_refs,
                            llm_log, qa_rounds, created_at, updated_at)
                           VALUES (?, ?, ?, ?, '{}', '[]', ?, '[]', ?, ?, ?)""",
                        (
                            str(uuid.uuid4()), doc.id, stage_name, state,
                            json.dumps(output_refs),
                            json.dumps(qa_rounds),
                            doc.created_at, now,
                        ),
                    )
                # Ensure the current stage has a job row even if no stage_data entry yet
                if doc.current_stage not in doc.stage_data:
                    await self._conn.execute(
                        """INSERT OR IGNORE INTO jobs
                           (id, document_id, stage, state, config, input_refs, output_refs,
                            llm_log, qa_rounds, created_at, updated_at)
                           VALUES (?, ?, ?, ?, '{}', '[]', '[]', '[]', '[]', ?, ?)""",
                        (
                            str(uuid.uuid4()), doc.id, doc.current_stage,
                            doc.stage_state,
                            doc.created_at, now,
                        ),
                    )
        await self._conn.commit()

    async def list_documents_paginated(
        self,
        stages: Optional[list] = None,
        states: Optional[list] = None,
        sort: str = "pipeline",
        page_size: int = 50,
        page_token: Optional[dict] = None,  # decoded token: {k: sort_key, id: last_id}
    ) -> tuple[list[Document], Optional[str]]:
        """Return (page, next_page_token_encoded | None)."""
        from adapters.inbound.schemas import encode_page_token

        conditions = ["current_stage != 'deleted'"]
        params: list = []
        if stages:
            conditions.append(f"current_stage IN ({','.join('?'*len(stages))})")
            params.extend(stages)
        if states:
            conditions.append(f"stage_state IN ({','.join('?'*len(states))})")
            params.extend(states)

        # Sort config: (ORDER BY clause, cursor WHERE clause template, cursor key extractor)
        _sort_map = {
            "pipeline":     ("current_stage ASC, created_at ASC, id ASC",
                             "(current_stage, created_at, id) > (?, ?, ?)",
                             lambda d: [d.current_stage, d.created_at, d.id]),
            "created_asc":  ("created_at ASC, id ASC",
                             "(created_at, id) > (?, ?)",
                             lambda d: [d.created_at, d.id]),
            "created_desc": ("created_at DESC, id DESC",
                             "(created_at, id) < (?, ?)",
                             lambda d: [d.created_at, d.id]),
            "title_asc":    ("LOWER(COALESCE(title,'')) ASC, id ASC",
                             "(LOWER(COALESCE(title,'')), id) > (?, ?)",
                             lambda d: [(d.title or "").lower(), d.id]),
            "title_desc":   ("LOWER(COALESCE(title,'')) DESC, id DESC",
                             "(LOWER(COALESCE(title,'')), id) < (?, ?)",
                             lambda d: [(d.title or "").lower(), d.id]),
        }
        order_clause, cursor_where, key_fn = _sort_map.get(
            sort, _sort_map["pipeline"]
        )

        if page_token:
            k = page_token.get("k")
            last_id = page_token.get("id", "")
            cursor_vals = k if isinstance(k, list) else [k, last_id]
            conditions.append(cursor_where)
            params.extend(cursor_vals)

        sql = (
            f"SELECT * FROM documents WHERE {' AND '.join(conditions)}"
            f" ORDER BY {order_clause} LIMIT ?"
        )
        params.append(page_size + 1)

        async with self._conn.execute(sql, params) as cur:
            rows = await cur.fetchall()

        docs = [self._row_to_doc(r) for r in rows]
        has_more = len(docs) > page_size
        if has_more:
            docs = docs[:page_size]

        next_token = None
        if has_more and docs:
            last = docs[-1]
            k_val = key_fn(last)
            # k_val is [sort_key, id] or [s, t, id]; store as list
            next_token = encode_page_token(k_val[:-1] if len(k_val) > 1 else k_val[0], last.id)
        return docs, next_token

    async def get_events_paginated(
        self,
        document_id: str,
        page_size: int = 100,
        after_id: Optional[int] = None,
    ) -> tuple[list[dict], Optional[int]]:
        """Return (events, next_after_id | None)."""
        params: list = [document_id]
        where_extra = ""
        if after_id is not None:
            where_extra = " AND id > ?"
            params.append(after_id)
        async with self._conn.execute(
            f"SELECT id, timestamp, stage, event_type, data FROM stage_events"
            f" WHERE document_id=?{where_extra} ORDER BY id ASC LIMIT ?",
            params + [page_size + 1],
        ) as cur:
            rows = await cur.fetchall()

        events = [
            {
                "id": r["id"],
                "timestamp": r["timestamp"],
                "stage": r["stage"],
                "event_type": r["event_type"],
                "data": json.loads(r["data"]) if r["data"] else None,
            }
            for r in rows
        ]
        has_more = len(events) > page_size
        if has_more:
            events = events[:page_size]
        next_after = events[-1]["id"] if has_more and events else None
        return events, next_after

    async def kv_get(self, key: str) -> Optional[str]:
        async with self._conn.execute("SELECT value FROM key_value WHERE key=?", (key,)) as cur:
            row = await cur.fetchone()
            return row["value"] if row else None

    async def kv_set(self, key: str, value: str) -> None:
        await self._conn.execute(
            "INSERT INTO key_value (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
            (key, value),
        )
        await self._conn.commit()

    async def list_contexts(self) -> list[dict]:
        async with self._conn.execute(
            "SELECT id, name, text FROM contexts ORDER BY created_at ASC"
        ) as cur:
            return [{"id": r["id"], "name": r["name"], "text": r["text"]} for r in await cur.fetchall()]

    async def create_context_entry(self, name: str, text: str) -> dict:
        entry_id = str(uuid.uuid4())
        import datetime as _dt
        now = _dt.datetime.utcnow().isoformat() + "Z"
        await self._conn.execute(
            "INSERT INTO contexts (id, name, text, created_at) VALUES (?, ?, ?, ?)",
            (entry_id, name, text, now),
        )
        await self._conn.commit()
        return {"id": entry_id, "name": name, "text": text}

    async def update_context_entry(
        self, context_id: str, name: Optional[str] = None, text: Optional[str] = None
    ) -> Optional[dict]:
        sets, params = [], []
        if name is not None:
            sets.append("name = ?"); params.append(name)
        if text is not None:
            sets.append("text = ?"); params.append(text)
        if sets:
            params.append(context_id)
            await self._conn.execute(
                f"UPDATE contexts SET {', '.join(sets)} WHERE id=?", params
            )
            await self._conn.commit()
        async with self._conn.execute(
            "SELECT id, name, text FROM contexts WHERE id=?", (context_id,)
        ) as cur:
            row = await cur.fetchone()
            return {"id": row["id"], "name": row["name"], "text": row["text"]} if row else None

    async def delete_context_entry(self, context_id: str) -> bool:
        async with self._conn.execute("DELETE FROM contexts WHERE id=?", (context_id,)) as cur:
            deleted = cur.rowcount > 0
        await self._conn.commit()
        return deleted

    async def create_chat_session(self, context: str, top_k: int) -> dict:
        import datetime as _dt
        session_id = str(uuid.uuid4())
        now = _dt.datetime.utcnow().isoformat() + "Z"
        await self._conn.execute(
            "INSERT INTO chat_sessions (id, title, context, top_k, created_at, updated_at)"
            " VALUES (?, '', ?, ?, ?, ?)",
            (session_id, context, top_k, now, now),
        )
        await self._conn.commit()
        return {"id": session_id, "title": "", "context": context, "top_k": top_k,
                "created_at": now, "updated_at": now, "message_count": 0}

    async def get_chat_session(self, session_id: str) -> Optional[dict]:
        async with self._conn.execute(
            "SELECT * FROM chat_sessions WHERE id=?", (session_id,)
        ) as cur:
            row = await cur.fetchone()
            if not row:
                return None
        async with self._conn.execute(
            "SELECT COUNT(*) FROM chat_messages WHERE session_id=?", (session_id,)
        ) as cur:
            count_row = await cur.fetchone()
            message_count = count_row[0] if count_row else 0
        return {
            "id": row["id"], "title": row["title"], "context": row["context"],
            "top_k": row["top_k"], "created_at": row["created_at"], "updated_at": row["updated_at"],
            "message_count": message_count,
        }

    async def list_chat_sessions(
        self, page_size: int = 20, before_id: Optional[str] = None
    ) -> list[dict]:
        params: list = []
        where = ""
        if before_id is not None:
            async with self._conn.execute(
                "SELECT created_at FROM chat_sessions WHERE id=?", (before_id,)
            ) as cur:
                ref = await cur.fetchone()
            if ref:
                where = " WHERE (created_at, id) < (?, ?)"
                params = [ref["created_at"], before_id]
        sql = (
            "SELECT id, title, context, top_k, created_at, updated_at,"
            " (SELECT COUNT(*) FROM chat_messages WHERE session_id = chat_sessions.id) as message_count"
            f" FROM chat_sessions{where} ORDER BY created_at DESC, id DESC LIMIT ?"
        )
        params.append(page_size)
        async with self._conn.execute(sql, params) as cur:
            rows = await cur.fetchall()
        return [
            {
                "id": r["id"], "title": r["title"], "context": r["context"],
                "top_k": r["top_k"], "created_at": r["created_at"], "updated_at": r["updated_at"],
                "message_count": r["message_count"],
            }
            for r in rows
        ]

    async def update_chat_session(self, session_id: str, **kwargs) -> Optional[dict]:
        allowed = {"title", "context", "top_k", "updated_at"}
        sets, params = [], []
        for k, v in kwargs.items():
            if k in allowed:
                sets.append(f"{k} = ?")
                params.append(v)
        if sets:
            params.append(session_id)
            await self._conn.execute(
                f"UPDATE chat_sessions SET {', '.join(sets)} WHERE id=?", params
            )
            await self._conn.commit()
        return await self.get_chat_session(session_id)

    async def delete_chat_session(self, session_id: str) -> bool:
        async with self._conn.execute("DELETE FROM chat_sessions WHERE id=?", (session_id,)) as cur:
            deleted = cur.rowcount > 0
        await self._conn.commit()
        return deleted

    async def append_chat_message(
        self, session_id: str, role: str, content: str, sources: Optional[list] = None
    ) -> dict:
        import datetime as _dt
        now = _dt.datetime.utcnow().isoformat() + "Z"
        sources_json = json.dumps(sources) if sources is not None else None
        async with self._conn.execute(
            "INSERT INTO chat_messages (session_id, role, content, sources, created_at)"
            " VALUES (?, ?, ?, ?, ?)",
            (session_id, role, content, sources_json, now),
        ) as cur:
            row_id = cur.lastrowid
        await self._conn.commit()
        return {
            "id": row_id, "role": role, "content": content,
            "sources": sources, "created_at": now,
        }

    async def list_chat_messages(self, session_id: str) -> list[dict]:
        async with self._conn.execute(
            "SELECT id, role, content, sources, created_at FROM chat_messages"
            " WHERE session_id=? ORDER BY id ASC",
            (session_id,),
        ) as cur:
            rows = await cur.fetchall()
        return [
            {
                "id": r["id"], "role": r["role"], "content": r["content"],
                "sources": json.loads(r["sources"]) if r["sources"] else None,
                "created_at": r["created_at"],
            }
            for r in rows
        ]
