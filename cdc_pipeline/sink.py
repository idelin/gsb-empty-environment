"""Downstream sink with idempotent apply (dedup by idempotency_id).

Simulates the business-effect receiver: a separate SQLite DB plus a JSONL
audit file. Re-applying the same idempotency_id is a no-op, so redelivery
after a crash never duplicates business effects.
"""
from __future__ import annotations

import sqlite3
from pathlib import Path


class IdempotentSink:
    def __init__(self, db_path: str, audit_path: str):
        self._conn = sqlite3.connect(db_path)
        self._conn.execute(
            "CREATE TABLE IF NOT EXISTS applied ("
            " idempotency_id TEXT PRIMARY KEY, payload TEXT NOT NULL)"
        )
        self._conn.commit()
        self._audit_path = Path(audit_path)

    def close(self) -> None:
        self._conn.close()

    def apply(self, idempotency_id: str, payload: str) -> bool:
        """Apply business effect exactly once. Returns True if newly applied."""
        cursor = self._conn.execute(
            "INSERT OR IGNORE INTO applied (idempotency_id, payload) VALUES (?, ?)",
            (idempotency_id, payload),
        )
        self._conn.commit()
        if cursor.rowcount == 0:
            return False  # duplicate delivery: deduped, no repeated effect
        with self._audit_path.open("a", encoding="utf-8") as fh:
            fh.write(payload + "\n")
        return True

    def applied_ids(self) -> set:
        rows = self._conn.execute("SELECT idempotency_id FROM applied").fetchall()
        return {row[0] for row in rows}

    def count(self) -> int:
        return self._conn.execute("SELECT COUNT(*) FROM applied").fetchone()[0]
