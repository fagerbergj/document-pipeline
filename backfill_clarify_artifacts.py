#!/usr/bin/env python3
"""
Backfill clarified_text.md artifacts for all completed clarify jobs
that don't already have one.

Usage (inside container):
    python3 /app/backfill_clarify_artifacts.py
"""

import json
import os
import sqlite3
import uuid
from datetime import datetime, timezone
from pathlib import Path

DB_PATH    = os.environ.get("DB_PATH",    "/data/pipeline.db")
VAULT_PATH = os.environ.get("VAULT_PATH", "/vault")


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def main():
    conn = sqlite3.connect(DB_PATH)
    conn.row_factory = sqlite3.Row

    clarify_jobs = conn.execute(
        "SELECT * FROM jobs WHERE stage='clarify' AND status='done'"
    ).fetchall()
    print(f"Found {len(clarify_jobs)} done clarify jobs")

    created = 0
    skipped = 0

    for job in clarify_jobs:
        job_id      = job["id"]
        document_id = job["document_id"]

        # Skip if a clarified_text.md artifact already exists for this document
        existing = conn.execute(
            "SELECT id FROM artifacts WHERE document_id=? AND filename='clarified_text.md'",
            (document_id,),
        ).fetchone()
        if existing:
            skipped += 1
            continue

        runs = json.loads(job["runs"] or "[]")
        if not runs:
            print(f"  [WARN] Job {job_id[:8]}: no runs, skipping")
            continue

        latest_run = runs[-1]
        outputs    = latest_run.get("outputs", [])
        text       = next(
            (o["text"] for o in outputs if o.get("field") == "clarified_text" and o.get("text")),
            None,
        )
        if not text:
            print(f"  [WARN] Job {job_id[:8]}: no clarified_text output, skipping")
            continue

        artifact_id = str(uuid.uuid4())
        dest_dir    = Path(VAULT_PATH) / "artifacts" / artifact_id
        dest_dir.mkdir(parents=True, exist_ok=True)
        (dest_dir / "clarified_text.md").write_text(text, encoding="utf-8")

        ts = now_iso()
        conn.execute(
            """INSERT INTO artifacts
               (id, document_id, filename, content_type, created_job_id, created_at, updated_at)
               VALUES (?, ?, ?, ?, ?, ?, ?)""",
            (artifact_id, document_id, "clarified_text.md", "text/markdown", job_id, ts, ts),
        )
        print(f"  Created artifact {artifact_id[:8]} for doc {document_id[:8]}")
        created += 1

    conn.commit()
    conn.close()
    print(f"\nDone — created: {created}, already had artifact: {skipped}")


if __name__ == "__main__":
    main()
