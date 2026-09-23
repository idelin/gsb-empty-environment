"""SQLite-backed durable state for the transaction buffer.

Tables:
  meta        -- high-water mark (last contiguously consumed seq)
  wal         -- every accepted event, kept until its transaction finishes
  applied_tx  -- finished transactions (committed / rolled back) for dedup
  data        -- committed rows; the ONLY state readers can observe

A transaction's changes are written to ``data`` inside a single SQLite
transaction together with the WAL bookkeeping, so a commit is atomic and a
half transaction is never visible -- including after a restart.
"""
from __future__ import annotations

import json
import sqlite3
from typing import Any, Optional

from .events import CHANGE, COMMIT, ROLLBACK, Event, event_from_payload

SCHEMA = """
CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS wal (
    seq      INTEGER PRIMARY KEY,
    tx_id    TEXT NOT NULL,
    kind     TEXT NOT NULL,
    payload  TEXT NOT NULL,
    consumed INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS applied_tx (
    tx_id   TEXT PRIMARY KEY,
    seq     INTEGER NOT NULL,
    outcome TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS data (
    tbl   TEXT NOT NULL,
    pk    TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY (tbl, pk)
);
"""

OUTCOME_COMMITTED = "committed"
OUTCOME_ROLLED_BACK = "rolled_back"


class Store:
    def __init__(self, path: str = ":memory:"):
        self._conn = sqlite3.connect(path, isolation_level=None)
        self._conn.execute("PRAGMA journal_mode=WAL")
        self._conn.execute("PRAGMA foreign_keys=ON")
        self._conn.executescript(SCHEMA)
        self._conn.execute(
            "INSERT OR IGNORE INTO meta(key, value) VALUES ('last_seq', '0')"
        )

    def close(self) -> None:
        self._conn.close()

    # -- high-water mark -------------------------------------------------
    def get_last_seq(self) -> int:
        row = self._conn.execute(
            "SELECT value FROM meta WHERE key = 'last_seq'"
        ).fetchone()
        return int(row[0])

    def advance_last_seq(self, seq: int) -> None:
        with self._conn:
            self._set_last_seq(self._conn, seq)

    @staticmethod
    def _set_last_seq(conn: sqlite3.Connection, seq: int) -> None:
        conn.execute("UPDATE meta SET value = ? WHERE key = 'last_seq'", (str(seq),))

    # -- WAL --------------------------------------------------------------
    def insert_wal(self, event: Event, consumed: bool = False) -> bool:
        """Persist an event; returns False if the seq was already present.

        ``consumed=True`` stores a tombstone: the seq slot is filled so the
        contiguous sequence can advance, but the event itself is inert.
        """
        cur = self._conn.execute(
            "INSERT OR IGNORE INTO wal(seq, tx_id, kind, payload, consumed)"
            " VALUES (?, ?, ?, ?, ?)",
            (event.seq, event.tx_id, event.kind, json.dumps(event.to_dict()),
             int(consumed)),
        )
        return cur.rowcount > 0

    def wal_row(self, seq: int) -> Optional[tuple[Event, bool]]:
        row = self._conn.execute(
            "SELECT payload, consumed FROM wal WHERE seq = ?", (seq,)
        ).fetchone()
        if row is None:
            return None
        return event_from_payload(row[0]), bool(row[1])

    def has_pending_beyond(self, seq: int) -> bool:
        row = self._conn.execute(
            "SELECT 1 FROM wal WHERE seq > ? AND consumed = 0 LIMIT 1", (seq,)
        ).fetchone()
        return row is not None

    def unconsumed_change_count(self, tx_id: str) -> int:
        row = self._conn.execute(
            "SELECT COUNT(*) FROM wal WHERE tx_id = ? AND kind = ? AND consumed = 0",
            (tx_id, CHANGE),
        ).fetchone()
        return int(row[0])

    def buffered_change_count(self) -> int:
        row = self._conn.execute(
            "SELECT COUNT(*) FROM wal WHERE kind = ? AND consumed = 0", (CHANGE,)
        ).fetchone()
        return int(row[0])

    def fetch_tx_changes(self, tx_id: str) -> list[Event]:
        rows = self._conn.execute(
            "SELECT payload FROM wal WHERE tx_id = ? AND kind = ? AND consumed = 0"
            " ORDER BY seq",
            (tx_id, CHANGE),
        ).fetchall()
        return [event_from_payload(r[0]) for r in rows]

    # -- finished transactions -------------------------------------------
    def tx_outcome(self, tx_id: str) -> Optional[str]:
        row = self._conn.execute(
            "SELECT outcome FROM applied_tx WHERE tx_id = ?", (tx_id,)
        ).fetchone()
        return row[0] if row else None

    # -- atomic state transitions -----------------------------------------
    def apply_commit(self, commit: Event, changes: list[Event]) -> None:
        """Apply a transaction's changes and bookkeeping in ONE SQLite txn."""
        with self._conn:
            for change in changes:
                self._apply_change(self._conn, change)
            self._conn.execute(
                "UPDATE wal SET consumed = 1 WHERE tx_id = ?", (commit.tx_id,)
            )
            self._conn.execute(
                "INSERT INTO applied_tx(tx_id, seq, outcome) VALUES (?, ?, ?)",
                (commit.tx_id, commit.seq, OUTCOME_COMMITTED),
            )
            self._set_last_seq(self._conn, commit.seq)

    def apply_rollback(self, marker: Event) -> None:
        """Discard a transaction's buffered changes in ONE SQLite txn."""
        with self._conn:
            self._conn.execute(
                "UPDATE wal SET consumed = 1 WHERE tx_id = ?", (marker.tx_id,)
            )
            self._conn.execute(
                "INSERT INTO applied_tx(tx_id, seq, outcome) VALUES (?, ?, ?)",
                (marker.tx_id, marker.seq, OUTCOME_ROLLED_BACK),
            )
            self._set_last_seq(self._conn, marker.seq)

    @staticmethod
    def _apply_change(conn: sqlite3.Connection, change: Event) -> None:
        if change.op == "insert":
            conn.execute(
                "INSERT OR REPLACE INTO data(tbl, pk, value) VALUES (?, ?, ?)",
                (change.table, change.pk, json.dumps(change.after)),
            )
        elif change.op == "update":
            conn.execute(
                "UPDATE data SET value = ? WHERE tbl = ? AND pk = ?",
                (json.dumps(change.after), change.table, change.pk),
            )
        elif change.op == "delete":
            conn.execute(
                "DELETE FROM data WHERE tbl = ? AND pk = ?",
                (change.table, change.pk),
            )
        else:  # pragma: no cover - guarded by validation
            raise ValueError(f"unknown op: {change.op!r}")

    # -- committed reads (the only visible state) --------------------------
    def read(self, table: str, pk: str) -> Optional[Any]:
        row = self._conn.execute(
            "SELECT value FROM data WHERE tbl = ? AND pk = ?", (table, str(pk))
        ).fetchone()
        return json.loads(row[0]) if row else None

    def read_all(self, table: str) -> dict[str, Any]:
        rows = self._conn.execute(
            "SELECT pk, value FROM data WHERE tbl = ? ORDER BY pk", (table,)
        ).fetchall()
        return {pk: json.loads(value) for pk, value in rows}
