"""Durable SQLite store: offset, business projection, outbox, commit records.

A single logical commit is one ``BEGIN IMMEDIATE`` transaction that writes:

* ``commit_record``    - one row per committed logical batch;
* ``applied_change``   - one row per change (deduplication of inputs);
* ``projection``       - the business effect (before/after applied);
* ``outbox``           - the promised output (pending until acknowledged);
* ``meta``             - the input offset (high-water commit sequence).

The offset is therefore never advanced before the output promise is durable.
"""

from __future__ import annotations

import json
import sqlite3
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from .errors import (
    ConflictingChangeError,
    DuplicateChangeError,
    GapDetectedError,
    PlannerError,
    StaleBatchError,
)
from .model import Change, Output, change_uid, output_id

SCHEMA = """
CREATE TABLE IF NOT EXISTS meta (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    high_water_seq INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS commit_record (
    commit_seq      INTEGER PRIMARY KEY,
    txn_ids         TEXT NOT NULL,
    change_count    INTEGER NOT NULL,
    output_count    INTEGER NOT NULL,
    committed_at    TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS applied_change (
    uid          TEXT PRIMARY KEY,
    commit_seq   INTEGER NOT NULL,
    change_json  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS projection (
    table_name   TEXT NOT NULL,
    key          TEXT NOT NULL,
    value_json   TEXT NOT NULL,
    PRIMARY KEY (table_name, key)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS outbox (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    idem_id     TEXT NOT NULL UNIQUE,
    change_uid  TEXT NOT NULL UNIQUE,
    commit_seq  INTEGER NOT NULL,
    event_type  TEXT NOT NULL,
    payload     TEXT NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('pending', 'acked')),
    attempts    INTEGER NOT NULL DEFAULT 0,
    enqueued_at TEXT NOT NULL,
    last_error  TEXT
);
CREATE INDEX IF NOT EXISTS idx_outbox_pending
    ON outbox(commit_seq, id) WHERE status = 'pending';
"""

OutputPlanner = Callable[[Change], Output]


@dataclass(frozen=True, slots=True)
class PendingOutput:
    id: int
    idem_id: str
    change_uid: str
    commit_seq: int
    event_type: str
    payload: dict[str, Any]
    attempts: int
    last_error: str | None


def _default_planner(change: Change) -> Output:
    return Output(
        uid=change_uid(change),
        idem_id=output_id(change),
        event_type=f"{change.table}.{change.kind()}",
        payload={
            "txn_id": change.txn_id,
            "table": change.table,
            "key": change.key,
            "before": change.before,
            "after": change.after,
        },
    )


class Store:
    """Durable state for one pipeline instance (single writer)."""

    def __init__(self, conn: sqlite3.Connection):
        self._conn = conn

    @classmethod
    def open(cls, path: str) -> "Store":
        Path(path).parent.mkdir(parents=True, exist_ok=True)
        conn = sqlite3.connect(path)
        conn.execute("PRAGMA journal_mode=WAL")
        conn.execute("PRAGMA synchronous=FULL")
        conn.execute("PRAGMA foreign_keys=ON")
        conn.executescript(SCHEMA)
        conn.execute(
            "INSERT OR IGNORE INTO meta(id, high_water_seq) VALUES (1, 0)"
        )
        conn.commit()
        return cls(conn)

    def close(self) -> None:
        self._conn.close()

    def __enter__(self) -> "Store":
        return self

    def __exit__(self, *exc: object) -> None:
        self.close()

    @property
    def high_water_seq(self) -> int:
        row = self._conn.execute(
            "SELECT high_water_seq FROM meta WHERE id = 1"
        ).fetchone()
        return int(row[0])

    def commit_batch(
        self,
        changes: list[Change],
        committed_at: str,
        planner: OutputPlanner = _default_planner,
    ) -> list[Output]:
        """Atomically commit one logical batch (a fixture commit sequence).

        Either the offset, business effects, commit record, and all outputs
        become durable together, or nothing does.
        """
        if not changes:
            raise PlannerError("cannot commit an empty batch")
        seq = changes[0].commit_seq
        if any(c.commit_seq != seq for c in changes):
            raise PlannerError("a batch must hold changes of one commit_seq")

        conn = self._conn
        uids = [change_uid(c) for c in changes]
        if len(set(uids)) != len(uids):
            raise DuplicateChangeError(
                f"duplicate logical change inside batch seq={seq}"
            )

        try:
            conn.execute("BEGIN IMMEDIATE")
        except sqlite3.IntegrityError as exc:
            raise ConflictingChangeError(str(exc)) from exc

        try:
            hwm_row = conn.execute(
                "SELECT high_water_seq FROM meta WHERE id = 1"
            ).fetchone()
            high_water = int(hwm_row[0])
            if seq <= high_water:
                raise StaleBatchError(
                    f"batch seq={seq} is not after high-water {high_water}"
                )
            if seq != high_water + 1:
                raise GapDetectedError(
                    f"expected seq={high_water + 1} but got seq={seq}"
                )

            planned: list[Output] = []
            seen_idem: set[str] = set()
            for change in changes:
                try:
                    output = planner(change)
                except PlannerError:
                    raise
                except Exception as exc:
                    raise PlannerError(str(exc)) from exc
                self._validate_output(output, change_uid(change), seen_idem)
                seen_idem.add(output.idem_id)
                planned.append(output)

            for change, output in zip(changes, planned, strict=True):
                conn.execute(
                    "INSERT INTO applied_change(uid, commit_seq, change_json)"
                    " VALUES (?, ?, ?)",
                    (
                        change_uid(change),
                        seq,
                        json.dumps(
                            {
                                "txn_id": change.txn_id,
                                "table": change.table,
                                "key": change.key,
                                "before": change.before,
                                "after": change.after,
                            },
                            sort_keys=True,
                        ),
                    ),
                )
                if change.after is None:
                    conn.execute(
                        "DELETE FROM projection WHERE table_name = ? AND key = ?",
                        (change.table, change.key),
                    )
                else:
                    conn.execute(
                        "INSERT INTO projection(table_name, key, value_json)"
                        " VALUES (?, ?, ?)"
                        " ON CONFLICT(table_name, key) DO UPDATE SET"
                        " value_json = excluded.value_json",
                        (
                            change.table,
                            change.key,
                            json.dumps(change.after, sort_keys=True),
                        ),
                    )
                conn.execute(
                    "INSERT INTO outbox(idem_id, change_uid, commit_seq,"
                    " event_type, payload, status, attempts, enqueued_at)"
                    " VALUES (?, ?, ?, ?, ?, 'pending', 0, ?)",
                    (
                        output.idem_id,
                        output.uid,
                        seq,
                        output.event_type,
                        json.dumps(output.payload, sort_keys=True),
                        committed_at,
                    ),
                )

            conn.execute(
                "INSERT INTO commit_record(commit_seq, txn_ids, change_count,"
                " output_count, committed_at) VALUES (?, ?, ?, ?, ?)",
                (
                    seq,
                    json.dumps(sorted({c.txn_id for c in changes})),
                    len(changes),
                    len(planned),
                    committed_at,
                ),
            )
            conn.execute(
                "UPDATE meta SET high_water_seq = ? WHERE id = 1", (seq,)
            )
            conn.commit()
        except BaseException:
            conn.rollback()
            raise
        return planned

    @staticmethod
    def _validate_output(
        output: Output, expected_uid: str, seen_idem: set[str]
    ) -> None:
        if not isinstance(output, Output):
            raise PlannerError("planner must return Output instances")
        if output.uid != expected_uid:
            raise PlannerError(
                f"output uid {output.uid!r} does not match change {expected_uid!r}"
            )
        if not output.idem_id or not isinstance(output.idem_id, str):
            raise PlannerError("output idem_id must be a non-empty string")
        if output.idem_id in seen_idem:
            raise PlannerError(f"duplicate output idem_id {output.idem_id!r}")
        if not isinstance(output.payload, dict):
            raise PlannerError("output payload must be an object")

    def fetch_pending(self, limit: int = 100) -> list[PendingOutput]:
        rows = self._conn.execute(
            "SELECT id, idem_id, change_uid, commit_seq, event_type, payload,"
            " attempts, last_error FROM outbox WHERE status = 'pending'"
            " ORDER BY commit_seq, id LIMIT ?",
            (limit,),
        ).fetchall()
        return [
            PendingOutput(
                id=row[0],
                idem_id=row[1],
                change_uid=row[2],
                commit_seq=row[3],
                event_type=row[4],
                payload=json.loads(row[5]),
                attempts=int(row[6]),
                last_error=row[7],
            )
            for row in rows
        ]

    def record_attempt(
        self, outbox_id: int, error: str | None, timestamp: str
    ) -> None:
        """Persist that a delivery attempt failed or its ack was lost.

        The row stays ``pending`` so the batch remains retryable.
        """
        with self._conn:
            self._conn.execute(
                "UPDATE outbox SET attempts = attempts + 1, last_error = ?,"
                " enqueued_at = ? WHERE id = ? AND status = 'pending'",
                (error, timestamp, outbox_id),
            )

    def ack_many(self, outbox_ids: list[int]) -> None:
        if not outbox_ids:
            return
        marks = ",".join("?" for _ in outbox_ids)
        with self._conn:
            self._conn.execute(
                f"UPDATE outbox SET status = 'acked', last_error = NULL"
                f" WHERE id IN ({marks}) AND status = 'pending'",
                outbox_ids,
            )

    def pending_count(self) -> int:
        row = self._conn.execute(
            "SELECT count(*) FROM outbox WHERE status = 'pending'"
        ).fetchone()
        return int(row[0])

    def acked_count(self) -> int:
        row = self._conn.execute(
            "SELECT count(*) FROM outbox WHERE status = 'acked'"
        ).fetchone()
        return int(row[0])

    def committed_sequences(self) -> list[int]:
        rows = self._conn.execute(
            "SELECT commit_seq FROM commit_record ORDER BY commit_seq"
        ).fetchall()
        return [int(row[0]) for row in rows]

    def projection_snapshot(self) -> dict[tuple[str, str], dict[str, Any]]:
        rows = self._conn.execute(
            "SELECT table_name, key, value_json FROM projection"
        ).fetchall()
        return {
            (row[0], row[1]): json.loads(row[2])
            for row in rows
        }

    def recovery_info(self) -> dict[str, Any]:
        return {
            "high_water_seq": self.high_water_seq,
            "pending_outputs": self.pending_count(),
            "acked_outputs": self.acked_count(),
            "committed_sequences": self.committed_sequences(),
        }
