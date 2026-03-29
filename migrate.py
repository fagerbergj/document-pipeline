#!/usr/bin/env python3
"""
One-time migration: old schema → new schema (API model refactor).

Old:  documents(stage_state, current_stage, context_ref, stage_data, ...)
      chat_sessions(context, top_k, ...)
      chat_messages(id INTEGER, session_id, ...)
      [no jobs, no artifacts tables]

New:  documents(additional_context, linked_contexts, ...)
      jobs(id, document_id, stage, status, options, runs, ...)
      artifacts(id, document_id, filename, content_type, created_job_id, ...)
      chat_sessions(system_prompt, rag_retrieval, ...)
      chat_messages(id TEXT/UUID, external_id, session_id, ...)

Usage (inside container):
    python3 /app/migrate.py
    # or with custom paths:
    DB_PATH=/data/pipeline.db VAULT_PATH=/vault python3 /app/migrate.py
"""

import json
import os
import shutil
import sqlite3
import uuid
from datetime import datetime, timezone
from pathlib import Path

DB_PATH   = os.environ.get("DB_PATH",   "/data/pipeline.db")
VAULT_PATH = os.environ.get("VAULT_PATH", "/vault")
BACKUP_PATH = DB_PATH + ".pre-migration.bak"

# ---------------------------------------------------------------------------
# New schema DDL (mirrors adapters/outbound/sqlite.py)
# ---------------------------------------------------------------------------

NEW_SCHEMA = """
CREATE TABLE IF NOT EXISTS contexts (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    text TEXT NOT NULL,
    created_at TEXT NOT NULL
);

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
);

CREATE TABLE IF NOT EXISTS artifacts (
    id TEXT PRIMARY KEY,
    document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    filename TEXT NOT NULL,
    content_type TEXT NOT NULL,
    created_job_id TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

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
);

CREATE TABLE IF NOT EXISTS stage_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    timestamp TEXT NOT NULL,
    stage TEXT NOT NULL,
    event_type TEXT NOT NULL,
    data TEXT
);

CREATE TABLE IF NOT EXISTS chat_sessions (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    system_prompt TEXT NOT NULL DEFAULT '',
    rag_retrieval TEXT NOT NULL DEFAULT '{"enabled": true, "max_sources": 5, "minimum_score": 0.0}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS chat_messages (
    id TEXT PRIMARY KEY,
    external_id TEXT,
    session_id TEXT NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    sources TEXT,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS key_value (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_documents_hash      ON documents(content_hash);
CREATE INDEX IF NOT EXISTS idx_jobs_document       ON jobs(document_id, stage);
CREATE INDEX IF NOT EXISTS idx_jobs_status         ON jobs(status);
CREATE INDEX IF NOT EXISTS idx_artifacts_document  ON artifacts(document_id);
CREATE INDEX IF NOT EXISTS idx_events_document     ON stage_events(document_id, stage);
CREATE INDEX IF NOT EXISTS idx_chat_messages_session ON chat_messages(session_id, created_at ASC);
"""

# Stage order matches pipeline
STAGE_ORDER = ["ocr", "clarify", "classify", "embed"]

def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def build_run_for_stage(stage: str, stage_data: dict) -> dict:
    """Convert old stage_data[stage] dict into a new Run object."""
    sd = stage_data.get(stage, {})
    run_id = str(uuid.uuid4())
    ts = now_iso()

    inputs: list[dict] = []
    outputs: list[dict] = []
    confidence: str = "high"
    questions: list[dict] = []
    suggestions: dict = {}

    if stage == "ocr":
        ocr_raw = sd.get("ocr_raw") or ""
        outputs = [{"field": "ocr_raw", "text": ocr_raw}]

    elif stage == "clarify":
        clarified = sd.get("clarified_text") or ""
        outputs = [{"field": "clarified_text", "text": clarified}]
        confidence = sd.get("confidence") or "high"
        ctx_update = sd.get("document_context_update") or ""
        if ctx_update:
            suggestions["additional_context"] = ctx_update
        # Map clarification_requests to questions
        for req in (sd.get("clarification_requests") or []):
            questions.append({
                "segment":  req.get("segment", ""),
                "question": req.get("question", ""),
                "answer":   None,
            })
        # Fill in answers from qa_history if available
        for round_ in (sd.get("qa_history") or []):
            for resp in (round_.get("responses") or []):
                seg = resp.get("segment", "")
                ans = resp.get("answer")
                for q in questions:
                    if q["segment"] == seg and q["answer"] is None:
                        q["answer"] = ans
                        break

    elif stage == "classify":
        summary = sd.get("summary") or ""
        tags    = sd.get("tags") or []
        outputs = [
            {"field": "summary", "text": summary},
            {"field": "tags",    "text": json.dumps(tags)},
        ]
        confidence = sd.get("confidence") or "high"
        for req in (sd.get("clarification_requests") or []):
            questions.append({
                "segment":  req.get("segment", ""),
                "question": req.get("question", ""),
                "answer":   None,
            })

    elif stage == "embed":
        pass  # embed produces no text outputs worth migrating

    return {
        "id":          run_id,
        "inputs":      inputs,
        "outputs":     outputs,
        "confidence":  confidence,
        "questions":   questions,
        "suggestions": suggestions,
        "created_at":  ts,
        "updated_at":  ts,
    }


def migrate_artifact(old_png_path: str, vault_path: str) -> tuple[str, str] | tuple[None, None]:
    """
    Copy the PNG from its old location to <vault_path>/artifacts/<artifact_id>/<filename>.
    Returns (artifact_id, new_path) or (None, None) if source doesn't exist.
    """
    src = Path(old_png_path)
    if not src.exists():
        print(f"  [WARN] PNG not found, skipping artifact copy: {old_png_path}")
        return None, None

    artifact_id = str(uuid.uuid4())
    dest_dir = Path(vault_path) / "artifacts" / artifact_id
    dest_dir.mkdir(parents=True, exist_ok=True)
    dest = dest_dir / src.name
    shutil.copy2(src, dest)
    return artifact_id, str(dest)


def main():
    print(f"DB:    {DB_PATH}")
    print(f"Vault: {VAULT_PATH}")

    if not Path(DB_PATH).exists():
        print("ERROR: DB not found.")
        raise SystemExit(1)

    # Backup
    shutil.copy2(DB_PATH, BACKUP_PATH)
    print(f"Backup written to {BACKUP_PATH}")

    old = sqlite3.connect(BACKUP_PATH)
    old.row_factory = sqlite3.Row

    # Read all old data before touching anything
    old_docs     = [dict(r) for r in old.execute("SELECT * FROM documents")]
    old_contexts = [dict(r) for r in old.execute("SELECT * FROM contexts")]
    old_sessions = [dict(r) for r in old.execute("SELECT * FROM chat_sessions")]
    old_messages = [dict(r) for r in old.execute("SELECT * FROM chat_messages")]
    old_events   = [dict(r) for r in old.execute("SELECT * FROM stage_events")]
    old.close()

    print(f"Read: {len(old_docs)} documents, {len(old_contexts)} contexts, "
          f"{len(old_sessions)} chat sessions, {len(old_messages)} messages, "
          f"{len(old_events)} stage events")

    # Recreate DB
    Path(DB_PATH).unlink()
    new = sqlite3.connect(DB_PATH)
    new.executescript(NEW_SCHEMA)
    new.commit()
    print("New schema created.")

    ts = now_iso()

    # ── contexts (unchanged) ────────────────────────────────────────────────
    for ctx in old_contexts:
        new.execute(
            "INSERT INTO contexts (id, name, text, created_at) VALUES (?,?,?,?)",
            (ctx["id"], ctx["name"], ctx["text"], ctx["created_at"]),
        )
    print(f"Migrated {len(old_contexts)} contexts.")

    # ── documents + jobs + artifacts ────────────────────────────────────────
    jobs_created = 0
    artifacts_created = 0

    for doc in old_docs:
        stage_data = json.loads(doc.get("stage_data") or "{}")
        ingest_sd  = stage_data.get("_ingest", {})

        # additional_context: prefer last document_context_update, fallback to _ingest.document_context
        additional_context = ingest_sd.get("document_context") or ""
        for stage in STAGE_ORDER:
            update = stage_data.get(stage, {}).get("document_context_update")
            if update:
                additional_context = update

        # linked_contexts: old context_ref → single-element array (or empty)
        context_ref = doc.get("context_ref")
        linked_contexts = json.dumps([context_ref] if context_ref else [])

        # Determine new png_path (may be updated by artifact migration below)
        new_png_path = doc.get("png_path")

        # Artifact migration: copy PNG to new structure
        artifact_id = None
        if new_png_path:
            artifact_id, migrated_path = migrate_artifact(new_png_path, VAULT_PATH)
            if migrated_path:
                new_png_path = migrated_path
                artifacts_created += 1

        new.execute(
            """INSERT INTO documents
               (id, content_hash, created_at, updated_at, title, date_month,
                png_path, duplicate_of, additional_context, linked_contexts)
               VALUES (?,?,?,?,?,?,?,?,?,?)""",
            (
                doc["id"], doc["content_hash"], doc["created_at"], doc["updated_at"],
                doc.get("title"), doc.get("date_month"), new_png_path,
                doc.get("duplicate_of"), additional_context, linked_contexts,
            ),
        )

        # Insert artifact record
        if artifact_id and new_png_path:
            new.execute(
                """INSERT INTO artifacts
                   (id, document_id, filename, content_type, created_job_id, created_at, updated_at)
                   VALUES (?,?,?,?,?,?,?)""",
                (
                    artifact_id, doc["id"],
                    Path(new_png_path).name, "image/png",
                    None,  # source image — not created by a job
                    doc["created_at"], ts,
                ),
            )

        # Create jobs for each completed stage
        for stage in STAGE_ORDER:
            if stage not in stage_data:
                continue
            run = build_run_for_stage(stage, stage_data)
            job_id = str(uuid.uuid4())
            new.execute(
                """INSERT INTO jobs
                   (id, document_id, stage, status, options, runs, created_at, updated_at)
                   VALUES (?,?,?,?,?,?,?,?)""",
                (
                    job_id, doc["id"], stage, "done",
                    "{}", json.dumps([run]),
                    doc["created_at"], doc["updated_at"],
                ),
            )
            jobs_created += 1

    print(f"Migrated {len(old_docs)} documents, {jobs_created} jobs, {artifacts_created} artifacts.")

    # ── stage_events (keep as audit log) ────────────────────────────────────
    for ev in old_events:
        new.execute(
            """INSERT INTO stage_events
               (id, document_id, timestamp, stage, event_type, data)
               VALUES (?,?,?,?,?,?)""",
            (ev["id"], ev["document_id"], ev["timestamp"],
             ev["stage"], ev["event_type"], ev.get("data")),
        )
    print(f"Migrated {len(old_events)} stage events.")

    # ── chat_sessions ────────────────────────────────────────────────────────
    for s in old_sessions:
        top_k = s.get("top_k") or 5
        rag = json.dumps({"enabled": True, "max_sources": top_k, "minimum_score": 0.0})
        new.execute(
            """INSERT INTO chat_sessions
               (id, title, system_prompt, rag_retrieval, created_at, updated_at)
               VALUES (?,?,?,?,?,?)""",
            (s["id"], s.get("title") or "", s.get("context") or "", rag,
             s["created_at"], s["updated_at"]),
        )
    print(f"Migrated {len(old_sessions)} chat sessions.")

    # ── chat_messages ────────────────────────────────────────────────────────
    for m in old_messages:
        msg_id = str(uuid.uuid4())  # old id was INTEGER AUTOINCREMENT
        new.execute(
            """INSERT INTO chat_messages
               (id, external_id, session_id, role, content, sources, created_at)
               VALUES (?,?,?,?,?,?,?)""",
            (msg_id, None, m["session_id"], m["role"], m["content"],
             m.get("sources"), m["created_at"]),
        )
    print(f"Migrated {len(old_messages)} chat messages.")

    new.commit()
    new.close()
    print("Migration complete.")
    print(f"Old DB backed up at: {BACKUP_PATH}")


if __name__ == "__main__":
    main()
