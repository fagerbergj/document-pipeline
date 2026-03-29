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
    title TEXT,
    date_month TEXT,
    png_path TEXT,
    duplicate_of TEXT REFERENCES documents(id),
    additional_context TEXT NOT NULL DEFAULT '',
    linked_contexts TEXT NOT NULL DEFAULT '[]'
)
"""

_CREATE_ARTIFACTS = """
CREATE TABLE IF NOT EXISTS artifacts (
    id TEXT PRIMARY KEY,
    document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    filename TEXT NOT NULL,
    content_type TEXT NOT NULL,
    created_job_id TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)
"""

_CREATE_JOBS = """
CREATE TABLE IF NOT EXISTS jobs (
    id          TEXT PRIMARY KEY,
    document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    stage       TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending',
    options     TEXT NOT NULL DEFAULT '{}',
    runs        TEXT NOT NULL DEFAULT '[]',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE(document_id, stage)
)
"""

_CREATE_STAGE_EVENTS = """
CREATE TABLE IF NOT EXISTS stage_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    timestamp TEXT NOT NULL,
    stage TEXT NOT NULL,
    event_type TEXT NOT NULL,
    data TEXT
)
"""

_CREATE_CHAT_SESSIONS = """
CREATE TABLE IF NOT EXISTS chat_sessions (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    system_prompt TEXT NOT NULL DEFAULT '',
    rag_retrieval TEXT NOT NULL DEFAULT '{"enabled": true, "max_sources": 5, "minimum_score": 0.0}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)
"""

_CREATE_CHAT_MESSAGES = """
CREATE TABLE IF NOT EXISTS chat_messages (
    id TEXT PRIMARY KEY,
    external_id TEXT,
    session_id TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    sources TEXT,
    created_at TEXT NOT NULL
)
"""

_CREATE_KEY_VALUE = """
CREATE TABLE IF NOT EXISTS key_value (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
)
"""

_INDEXES = [
    "CREATE INDEX IF NOT EXISTS idx_documents_hash ON documents(content_hash)",
    "CREATE INDEX IF NOT EXISTS idx_jobs_document ON jobs(document_id, stage)",
    "CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status)",
    "CREATE INDEX IF NOT EXISTS idx_artifacts_document ON artifacts(document_id)",
    "CREATE INDEX IF NOT EXISTS idx_events_document ON stage_events(document_id, stage)",
    "CREATE INDEX IF NOT EXISTS idx_chat_messages_session ON chat_messages(session_id, created_at ASC)",
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
        await self._conn.execute(_CREATE_ARTIFACTS)
        await self._conn.execute(_CREATE_JOBS)
        await self._conn.execute(_CREATE_STAGE_EVENTS)
        await self._conn.execute(_CREATE_CHAT_SESSIONS)
        await self._conn.execute(_CREATE_CHAT_MESSAGES)
        await self._conn.execute(_CREATE_KEY_VALUE)
        for idx in _INDEXES:
            await self._conn.execute(idx)
        await self._conn.commit()

    async def close(self):
        if self._conn:
            await self._conn.close()

    # ── Row deserializers ──────────────────────────────────────────────────────

    def _row_to_doc(self, row) -> Document:
        d = dict(row)
        return Document(
            id=d["id"],
            content_hash=d["content_hash"],
            created_at=d["created_at"],
            updated_at=d["updated_at"],
            title=d["title"],
            date_month=d["date_month"],
            png_path=d["png_path"],
            duplicate_of=d["duplicate_of"],
            additional_context=d.get("additional_context") or "",
            linked_contexts=json.loads(d.get("linked_contexts") or "[]"),
        )

    def _row_to_job(self, row) -> Job:
        d = dict(row)
        return Job(
            id=d["id"],
            document_id=d["document_id"],
            stage=d["stage"],
            status=d["status"],
            options=json.loads(d.get("options") or "{}"),
            runs=json.loads(d.get("runs") or "[]"),
            created_at=d["created_at"],
            updated_at=d["updated_at"],
        )

    # ── Documents ──────────────────────────────────────────────────────────────

    async def get_by_hash(self, content_hash: str) -> Optional[Document]:
        async with self._conn.execute(
            "SELECT * FROM documents WHERE content_hash = ?", (content_hash,)
        ) as cur:
            row = await cur.fetchone()
            return self._row_to_doc(row) if row else None

    async def insert(self, doc: Document):
        await self._conn.execute(
            """INSERT INTO documents
               (id, content_hash, created_at, updated_at, title, date_month,
                png_path, duplicate_of, additional_context, linked_contexts)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
            (
                doc.id, doc.content_hash, doc.created_at, doc.updated_at,
                doc.title, doc.date_month, doc.png_path, doc.duplicate_of,
                doc.additional_context, json.dumps(doc.linked_contexts),
            ),
        )
        await self._conn.commit()

    async def update(self, doc: Document):
        await self._conn.execute(
            """UPDATE documents SET
               updated_at=?, title=?, date_month=?, png_path=?, duplicate_of=?,
               additional_context=?, linked_contexts=?
               WHERE id=?""",
            (
                doc.updated_at, doc.title, doc.date_month, doc.png_path,
                doc.duplicate_of, doc.additional_context,
                json.dumps(doc.linked_contexts), doc.id,
            ),
        )
        await self._conn.commit()

    async def get(self, doc_id: str) -> Optional[Document]:
        async with self._conn.execute(
            "SELECT * FROM documents WHERE id=?", (doc_id,)
        ) as cur:
            row = await cur.fetchone()
            return self._row_to_doc(row) if row else None

    async def delete(self, document_id: str) -> None:
        await self._conn.execute("DELETE FROM documents WHERE id=?", (document_id,))
        await self._conn.commit()

    async def list_documents_paginated(
        self,
        sort: str = "pipeline",
        page_size: int = 20,
        page_token: Optional[dict] = None,
        stages: Optional[list] = None,
        statuses: Optional[list] = None,
    ) -> tuple[list[Document], Optional[str]]:
        from adapters.inbound.schemas import encode_page_token

        conditions: list[str] = []
        params: list = []

        # Filter by current job stage/status using a correlated subquery
        # "current job" = lowest priority number (running < waiting < pending < error < done)
        _current_job_sql = """(
            SELECT j.{col} FROM jobs j WHERE j.document_id = d.id
            ORDER BY CASE j.status
                WHEN 'running'  THEN 0 WHEN 'waiting' THEN 1
                WHEN 'pending'  THEN 2 WHEN 'error'   THEN 3
                ELSE 4 END, j.updated_at DESC
            LIMIT 1
        )"""
        if stages:
            conditions.append(
                f"{_current_job_sql.format(col='stage')} IN ({','.join('?'*len(stages))})"
            )
            params.extend(stages)
        if statuses:
            conditions.append(
                f"{_current_job_sql.format(col='status')} IN ({','.join('?'*len(statuses))})"
            )
            params.extend(statuses)

        _sort_map = {
            "pipeline":     ("d.created_at ASC, d.id ASC",
                             "(d.created_at, d.id) > (?, ?)",
                             lambda d: [d.created_at, d.id]),
            "created_asc":  ("d.created_at ASC, d.id ASC",
                             "(d.created_at, d.id) > (?, ?)",
                             lambda d: [d.created_at, d.id]),
            "created_desc": ("d.created_at DESC, d.id DESC",
                             "(d.created_at, d.id) < (?, ?)",
                             lambda d: [d.created_at, d.id]),
            "title_asc":    ("LOWER(COALESCE(d.title,'')) ASC, d.id ASC",
                             "(LOWER(COALESCE(d.title,'')), d.id) > (?, ?)",
                             lambda d: [(d.title or "").lower(), d.id]),
            "title_desc":   ("LOWER(COALESCE(d.title,'')) DESC, d.id DESC",
                             "(LOWER(COALESCE(d.title,'')), d.id) < (?, ?)",
                             lambda d: [(d.title or "").lower(), d.id]),
        }
        order_clause, cursor_where, key_fn = _sort_map.get(sort, _sort_map["pipeline"])

        if page_token:
            k = page_token.get("k")
            last_id = page_token.get("id", "")
            cursor_vals = (k if isinstance(k, list) else [k]) + [last_id]
            conditions.append(cursor_where)
            params.extend(cursor_vals)

        where = f"WHERE {' AND '.join(conditions)}" if conditions else ""
        sql = f"SELECT d.* FROM documents d {where} ORDER BY {order_clause} LIMIT ?"
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
            next_token = encode_page_token(k_val[:-1] if len(k_val) > 1 else k_val[0], last.id)
        return docs, next_token

    # ── Artifacts ──────────────────────────────────────────────────────────────

    async def insert_artifact(
        self,
        document_id: str,
        filename: str,
        content_type: str,
        created_job_id: Optional[str],
        now: str,
        artifact_id: Optional[str] = None,
    ) -> dict:
        artifact_id = artifact_id or str(uuid.uuid4())
        await self._conn.execute(
            """INSERT INTO artifacts (id, document_id, filename, content_type, created_job_id, created_at, updated_at)
               VALUES (?, ?, ?, ?, ?, ?, ?)""",
            (artifact_id, document_id, filename, content_type, created_job_id, now, now),
        )
        await self._conn.commit()
        return {
            "id": artifact_id, "document_id": document_id, "filename": filename,
            "content_type": content_type, "created_job_id": created_job_id,
            "created_at": now, "updated_at": now,
        }

    async def list_artifacts(self, document_id: str) -> list[dict]:
        async with self._conn.execute(
            "SELECT * FROM artifacts WHERE document_id=? ORDER BY created_at ASC",
            (document_id,),
        ) as cur:
            rows = await cur.fetchall()
        return [
            {
                "id": r["id"], "document_id": r["document_id"], "filename": r["filename"],
                "content_type": r["content_type"], "created_job_id": r["created_job_id"],
                "created_at": r["created_at"], "updated_at": r["updated_at"],
            }
            for r in rows
        ]

    async def get_artifact(self, document_id: str, artifact_id: str) -> Optional[dict]:
        async with self._conn.execute(
            "SELECT * FROM artifacts WHERE id=? AND document_id=?",
            (artifact_id, document_id),
        ) as cur:
            row = await cur.fetchone()
        if not row:
            return None
        return {
            "id": row["id"], "document_id": row["document_id"], "filename": row["filename"],
            "content_type": row["content_type"], "created_job_id": row["created_job_id"],
            "created_at": row["created_at"], "updated_at": row["updated_at"],
        }

    # ── Jobs ──────────────────────────────────────────────────────────────────

    async def upsert_job(self, job: Job) -> None:
        await self._conn.execute(
            """INSERT INTO jobs (id, document_id, stage, status, options, runs, created_at, updated_at)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?)
               ON CONFLICT(document_id, stage) DO UPDATE SET
                 status=excluded.status,
                 options=excluded.options,
                 runs=excluded.runs,
                 updated_at=excluded.updated_at""",
            (
                job.id, job.document_id, job.stage, job.status,
                json.dumps(job.options), json.dumps(job.runs),
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

    async def update_job_status(self, job_id: str, status: str, now: str) -> None:
        await self._conn.execute(
            "UPDATE jobs SET status=?, updated_at=? WHERE id=?",
            (status, now, job_id),
        )
        await self._conn.commit()

    async def update_job_options(self, job_id: str, options: dict, now: str) -> None:
        await self._conn.execute(
            "UPDATE jobs SET options=?, updated_at=? WHERE id=?",
            (json.dumps(options), now, job_id),
        )
        await self._conn.commit()

    async def update_job_runs(self, job_id: str, runs: list, now: str) -> None:
        await self._conn.execute(
            "UPDATE jobs SET runs=?, updated_at=? WHERE id=?",
            (json.dumps(runs), now, job_id),
        )
        await self._conn.commit()

    async def append_run(self, job_id: str, run: dict, now: str) -> None:
        """Append a run to job.runs and update updated_at."""
        async with self._conn.execute(
            "SELECT runs FROM jobs WHERE id=?", (job_id,)
        ) as cur:
            row = await cur.fetchone()
        if not row:
            return
        runs = json.loads(row["runs"] or "[]")
        runs.append(run)
        await self._conn.execute(
            "UPDATE jobs SET runs=?, updated_at=? WHERE id=?",
            (json.dumps(runs), now, job_id),
        )
        await self._conn.commit()

    async def patch_run(self, job_id: str, run_id: str, patch: dict, now: str) -> dict | None:
        """Update a specific run by its id. Returns the updated run dict or None if not found."""
        async with self._conn.execute(
            "SELECT runs FROM jobs WHERE id=?", (job_id,)
        ) as cur:
            row = await cur.fetchone()
        if not row:
            return None
        runs = json.loads(row["runs"] or "[]")
        for run in runs:
            if str(run.get("id")) == str(run_id):
                run.update(patch)
                run["updated_at"] = now
                await self._conn.execute(
                    "UPDATE jobs SET runs=?, updated_at=? WHERE id=?",
                    (json.dumps(runs), now, job_id),
                )
                await self._conn.commit()
                return run
        return None

    async def list_jobs_for_document(self, document_id: str) -> list[Job]:
        async with self._conn.execute(
            "SELECT * FROM jobs WHERE document_id=? ORDER BY created_at ASC",
            (document_id,),
        ) as cur:
            return [self._row_to_job(r) for r in await cur.fetchall()]

    async def get_pending_jobs(self, stage: str) -> list[Job]:
        """Return all jobs in pending status for a given stage, ordered by created_at."""
        async with self._conn.execute(
            "SELECT * FROM jobs WHERE stage=? AND status='pending' ORDER BY created_at ASC",
            (stage,),
        ) as cur:
            return [self._row_to_job(r) for r in await cur.fetchall()]

    async def reset_running(self) -> int:
        """On startup, reset jobs stuck in 'running' back to 'pending'."""
        async with self._conn.execute(
            "UPDATE jobs SET status='pending' WHERE status='running'"
        ) as cur:
            count = cur.rowcount
        await self._conn.commit()
        return count

    async def cascade_replay(self, document_id: str, from_stage: str, stage_order: list[str], now: str) -> None:
        """Set all downstream jobs (after from_stage) to pending."""
        if from_stage not in stage_order:
            return
        idx = stage_order.index(from_stage)
        downstream = stage_order[idx + 1:]
        if not downstream:
            return
        placeholders = ",".join("?" * len(downstream))
        await self._conn.execute(
            f"UPDATE jobs SET status='pending', updated_at=? WHERE document_id=? AND stage IN ({placeholders})",
            [now, document_id] + downstream,
        )
        await self._conn.commit()

    async def list_jobs_paginated(
        self,
        job_id: Optional[list[str]] = None,
        document_id: Optional[str | list[str]] = None,
        stages: Optional[list] = None,
        statuses: Optional[list] = None,
        sort: str = "pipeline",
        page_size: int = 50,
        page_token: Optional[dict] = None,
    ) -> tuple[list[Job], Optional[str]]:
        from adapters.inbound.schemas import encode_page_token

        conditions: list[str] = []
        params: list = []

        if job_id:
            conditions.append(f"id IN ({','.join('?'*len(job_id))})")
            params.extend(job_id)
        if document_id:
            ids = [document_id] if isinstance(document_id, str) else document_id
            conditions.append(f"document_id IN ({','.join('?'*len(ids))})")
            params.extend(ids)
        if stages:
            conditions.append(f"stage IN ({','.join('?'*len(stages))})")
            params.extend(stages)
        if statuses:
            conditions.append(f"status IN ({','.join('?'*len(statuses))})")
            params.extend(statuses)

        _sort_map = {
            "pipeline":     ("created_at ASC, id ASC",
                             "(created_at, id) > (?, ?)",
                             lambda j: [j.created_at, j.id]),
            "created_asc":  ("created_at ASC, id ASC",
                             "(created_at, id) > (?, ?)",
                             lambda j: [j.created_at, j.id]),
            "created_desc": ("created_at DESC, id DESC",
                             "(created_at, id) < (?, ?)",
                             lambda j: [j.created_at, j.id]),
            "title_asc":    ("created_at ASC, id ASC",
                             "(created_at, id) > (?, ?)",
                             lambda j: [j.created_at, j.id]),
            "title_desc":   ("created_at ASC, id ASC",
                             "(created_at, id) > (?, ?)",
                             lambda j: [j.created_at, j.id]),
        }
        order_clause, cursor_where, key_fn = _sort_map.get(sort, _sort_map["pipeline"])

        if page_token:
            k = page_token.get("k")
            last_id = page_token.get("id", "")
            cursor_vals = (k if isinstance(k, list) else [k]) + [last_id]
            conditions.append(cursor_where)
            params.extend(cursor_vals)

        where = f"WHERE {' AND '.join(conditions)}" if conditions else ""
        sql = f"SELECT * FROM jobs {where} ORDER BY {order_clause} LIMIT ?"
        params.append(page_size + 1)

        async with self._conn.execute(sql, params) as cur:
            rows = await cur.fetchall()

        jobs = [self._row_to_job(r) for r in rows]
        has_more = len(jobs) > page_size
        if has_more:
            jobs = jobs[:page_size]

        next_token = None
        if has_more and jobs:
            last = jobs[-1]
            k_val = key_fn(last)
            next_token = encode_page_token(k_val[:-1] if len(k_val) > 1 else k_val[0], last.id)
        return jobs, next_token

    # ── Stage events (internal audit log) ─────────────────────────────────────

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

    # ── Key-value store ────────────────────────────────────────────────────────

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

    # ── Contexts ──────────────────────────────────────────────────────────────

    async def list_contexts(self) -> list[dict]:
        async with self._conn.execute(
            "SELECT id, name, text FROM contexts ORDER BY created_at ASC"
        ) as cur:
            return [{"id": r["id"], "name": r["name"], "text": r["text"]} for r in await cur.fetchall()]

    async def get_context(self, context_id: str) -> Optional[dict]:
        async with self._conn.execute(
            "SELECT id, name, text FROM contexts WHERE id=?", (context_id,)
        ) as cur:
            row = await cur.fetchone()
            return {"id": row["id"], "name": row["name"], "text": row["text"]} if row else None

    async def create_context_entry(self, name: str, text: str) -> dict:
        import datetime as _dt
        entry_id = str(uuid.uuid4())
        now = _dt.datetime.now(_dt.timezone.utc).isoformat()
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
        return await self.get_context(context_id)

    async def delete_context_entry(self, context_id: str) -> bool:
        async with self._conn.execute("DELETE FROM contexts WHERE id=?", (context_id,)) as cur:
            deleted = cur.rowcount > 0
        await self._conn.commit()
        return deleted

    # ── Chat ──────────────────────────────────────────────────────────────────

    async def create_chat(self, system_prompt: str, rag_retrieval: dict) -> dict:
        import datetime as _dt
        chat_id = str(uuid.uuid4())
        now = _dt.datetime.now(_dt.timezone.utc).isoformat()
        await self._conn.execute(
            "INSERT INTO chat_sessions (id, title, system_prompt, rag_retrieval, created_at, updated_at)"
            " VALUES (?, '', ?, ?, ?, ?)",
            (chat_id, system_prompt, json.dumps(rag_retrieval), now, now),
        )
        await self._conn.commit()
        return {
            "id": chat_id, "title": "", "system_prompt": system_prompt,
            "rag_retrieval": rag_retrieval, "created_at": now, "updated_at": now,
        }

    async def get_chat(self, chat_id: str) -> Optional[dict]:
        async with self._conn.execute(
            "SELECT * FROM chat_sessions WHERE id=?", (chat_id,)
        ) as cur:
            row = await cur.fetchone()
            if not row:
                return None
        return {
            "id": row["id"], "title": row["title"], "system_prompt": row["system_prompt"],
            "rag_retrieval": json.loads(row["rag_retrieval"]),
            "created_at": row["created_at"], "updated_at": row["updated_at"],
        }

    async def list_chats(self, page_size: int = 20, before_id: Optional[str] = None) -> list[dict]:
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
            "SELECT id, title, system_prompt, rag_retrieval, created_at, updated_at"
            f" FROM chat_sessions{where} ORDER BY created_at DESC, id DESC LIMIT ?"
        )
        params.append(page_size)
        async with self._conn.execute(sql, params) as cur:
            rows = await cur.fetchall()
        return [
            {
                "id": r["id"], "title": r["title"], "system_prompt": r["system_prompt"],
                "rag_retrieval": json.loads(r["rag_retrieval"]),
                "created_at": r["created_at"], "updated_at": r["updated_at"],
            }
            for r in rows
        ]

    async def update_chat(self, chat_id: str, **kwargs) -> Optional[dict]:
        import datetime as _dt
        allowed = {"title", "system_prompt", "rag_retrieval"}
        sets, params = [], []
        for k, v in kwargs.items():
            if k in allowed:
                sets.append(f"{k} = ?")
                params.append(json.dumps(v) if k == "rag_retrieval" else v)
        if sets:
            now = _dt.datetime.now(_dt.timezone.utc).isoformat()
            sets.append("updated_at = ?")
            params.append(now)
            params.append(chat_id)
            await self._conn.execute(
                f"UPDATE chat_sessions SET {', '.join(sets)} WHERE id=?", params
            )
            await self._conn.commit()
        return await self.get_chat(chat_id)

    async def delete_chat(self, chat_id: str) -> bool:
        async with self._conn.execute("DELETE FROM chat_sessions WHERE id=?", (chat_id,)) as cur:
            deleted = cur.rowcount > 0
        await self._conn.commit()
        return deleted

    async def append_chat_message(
        self, session_id: str, role: str, content: str,
        sources: Optional[list] = None, external_id: Optional[str] = None,
    ) -> dict:
        import datetime as _dt
        message_id = str(uuid.uuid4())
        now = _dt.datetime.now(_dt.timezone.utc).isoformat()
        sources_json = json.dumps(sources) if sources is not None else None
        await self._conn.execute(
            "INSERT INTO chat_messages (id, external_id, session_id, role, content, sources, created_at)"
            " VALUES (?, ?, ?, ?, ?, ?, ?)",
            (message_id, external_id, session_id, role, content, sources_json, now),
        )
        await self._conn.commit()
        return {
            "id": message_id, "external_id": external_id, "role": role,
            "content": content, "sources": sources, "created_at": now,
        }

    async def list_chat_messages(self, session_id: str) -> list[dict]:
        async with self._conn.execute(
            "SELECT id, external_id, role, content, sources, created_at FROM chat_messages"
            " WHERE session_id=? ORDER BY created_at ASC, id ASC",
            (session_id,),
        ) as cur:
            rows = await cur.fetchall()
        return [
            {
                "id": r["id"], "external_id": r["external_id"], "role": r["role"],
                "content": r["content"],
                "sources": json.loads(r["sources"]) if r["sources"] else None,
                "created_at": r["created_at"],
            }
            for r in rows
        ]
