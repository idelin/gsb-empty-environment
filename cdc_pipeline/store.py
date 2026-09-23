"""Local state store (SQLite, stdlib only).

Holds, in ONE database:
  - offsets:  confirmed input position per source
  - outbox:   generated outputs awaiting delivery/ack

Offset advancement and outbox insertion happen in the SAME transaction,
so a crash can never persist one without the other.
"""
from __future__ import annotations

import sqlite3
from typing import List, Optional

from .models import OutboxRecord

_SCHEMA = """
CREATE TABLE IF NOT EXISTS offsets (
    source      TEXT PRIMARY KEY,
    commit_seq  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS outbox (
    idempotency_id TEXT PRIMARY KEY,
    payload        TEXT NOT NULL,
    status         TEXT NOT NULL CHECK (status IN ('pending', 'acked')),
    commit_seq     INTEGER NOT NULL
);
"""


class StateStore:
    def __init__(self, db_path: str):
        self._conn = sqlite3.connect(db_path)
        self._conn.execute("PRAGMA journal_mode=WAL")
        self._conn.execute("PRAGMA synchronous=FULL")
        self._conn.executescript(_SCHEMA)
        self._conn.commit()

    def close(self) -> None:
        self._conn.close()

    def get_offset(self, source: str) -> int:
        row = self._conn.execute(
            "SELECT commit_seq FROM offsets WHERE source = ?", (source,)
        ).fetchone()
        return row[0] if row else 0

    def commit_batch(self, source: str, records: List[OutboxRecord], new_offset: int) -> None:
        """Atomically: insert pending outputs + advance the input offset.

        If anything fails, the whole transaction rolls back: the offset does
        not move and the batch stays fully retryable.
        """
        try:
            self._conn.execute("BEGIN IMMEDIATE")
            self._conn.executemany(
                "INSERT OR IGNORE INTO outbox (idempotency_id, payload, status, commit_seq)"
                " VALUES (?, ?, 'pending', ?)",
                [(r.idempotency_id, r.payload, r.commit_seq) for r in records],
            )
            self._conn.execute(
                "INSERT INTO offsets (source, commit_seq) VALUES (?, ?)"
                " ON CONFLICT(source) DO UPDATE SET commit_seq = excluded.commit_seq",
                (source, new_offset),
            )
            self._conn.commit()
        except Exception:
            self._conn.rollback()
            raise

    def pending_outputs(self) -> List[OutboxRecord]:
        rows = self._conn.execute(
            "SELECT idempotency_id, payload, status, commit_seq"
            " FROM outbox WHERE status = 'pending' ORDER BY commit_seq, idempotency_id"
        ).fetchall()
        return [OutboxRecord(*row) for row in rows]

    def mark_acked(self, idempotency_id: str) -> None:
        self._conn.execute(
            "UPDATE outbox SET status = 'acked' WHERE idempotency_id = ?",
            (idempotency_id,),
        )
        self._conn.commit()

    def outbox_counts(self) -> dict:
        rows = self._conn.execute(
            "SELECT status, COUNT(*) FROM outbox GROUP BY status"
        ).fetchall()
        return {status: count for status, count in rows}
