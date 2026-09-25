"""Durable high-water mark storage with explicit crash injection.

The durable log is a small append-only table kept in the local file system
via the standard-library ``sqlite3`` module.  Two crash points are modelled
inside the durability window of a publication:

* ``BEFORE_SYNC`` -- the process dies after staging records but before the
  fsync/commit; nothing survives and no tag was published;
* ``AFTER_SYNC``  -- the process dies immediately after the commit returned
  but before anyone was notified; the tag survived and must be reused rather
  than minted again.

A third crash point, ``AFTER_PUBLISH``, is handled one layer up (the tag is
committed and the message left the outbox, but the publication
acknowledgement never landed).

Every process incarnation durably records its epoch at start-up, so a
restarted process always knows the next epoch even when the lost event was
the very first one.
"""
from __future__ import annotations

import json
import os
import sqlite3
from dataclasses import dataclass, field
from enum import Enum
from typing import Optional

from .tag import Tag


class CrashPoint(Enum):
    BEFORE_SYNC = "before_sync"
    AFTER_SYNC = "after_sync"
    AFTER_PUBLISH = "after_publish"


class InjectedCrash(RuntimeError):
    def __init__(self, point: CrashPoint, detail: str = "") -> None:
        super().__init__(f"injected crash at {point.value}: {detail}")
        self.point = point


@dataclass
class PendingSend:
    tag: Tag
    target: str
    payload: str

@dataclass
class RecoveryState:
    epoch: int
    physical_floor: int
    pending_sends: dict[str, PendingSend] = field(default_factory=dict)
    seen_messages: set[str] = field(default_factory=set)
    incarnation_count: int = 0


class SqliteDurableStore:
    """Append-only durable log backed by a single SQLite file."""

    def __init__(self, path: str, crash: Optional[CrashPoint] = None) -> None:
        self.path = path
        self._crash = crash
        self._conn = sqlite3.connect(path)
        self._conn.execute("PRAGMA journal_mode=WAL")
        self._conn.execute("PRAGMA synchronous=FULL")
        self._conn.execute(
            """
            CREATE TABLE IF NOT EXISTS records (
                id      INTEGER PRIMARY KEY AUTOINCREMENT,
                kind    TEXT NOT NULL,
                msg_id  TEXT,
                payload TEXT NOT NULL
            )
            """
        )
        self._conn.commit()

    @property
    def pending_crash(self) -> Optional[CrashPoint]:
        return self._crash

    def consume_crash(self) -> Optional[CrashPoint]:
        point = self._crash
        self._crash = None
        return point

    def commit_sync(self, records: list[dict], crash: Optional[CrashPoint] = None) -> None:
        """Atomically append ``records``, honouring an injected crash."""
        if crash is None:
            crash = self._crash
        cur = self._conn.cursor()
        try:
            cur.execute("BEGIN")
            for rec in records:
                kind = rec["kind"]
                msg_id = rec.get("msg_id")
                payload = {k: v for k, v in rec.items() if k not in ("kind", "msg_id")}
                cur.execute(
                    "INSERT INTO records(kind, msg_id, payload) VALUES (?, ?, ?)",
                    (kind, msg_id, json.dumps(payload, sort_keys=True)),
                )
            if crash is CrashPoint.BEFORE_SYNC:
                cur.execute("ROLLBACK")
                raise InjectedCrash(CrashPoint.BEFORE_SYNC, "records staged, never synced")
            self._conn.commit()
            if crash is CrashPoint.AFTER_SYNC:
                raise InjectedCrash(CrashPoint.AFTER_SYNC, "commit durable, process died")
        finally:
            self._crash = None

    def recover(self) -> RecoveryState:
        rows = self._conn.execute(
            "SELECT kind, msg_id, payload FROM records ORDER BY id"
        ).fetchall()
        max_epoch = -1
        floor = -1
        incarnations = 0
        pending: dict[str, Tag] = {}
        seen: set[str] = set()
        for kind, msg_id, payload_text in rows:
            payload = json.loads(payload_text)
            if kind == "incarnation":
                incarnations += 1
                max_epoch = max(max_epoch, int(payload["epoch"]))
            elif kind == "tag":
                tag = Tag.from_dict(payload["tag"])
                max_epoch = max(max_epoch, tag.epoch)
                floor = max(floor, tag.physical)
            elif kind == "send":
                pending[str(msg_id)] = PendingSend(
                    tag=Tag.from_dict(payload["tag"]),
                    target=str(payload["target"]),
                    payload=str(payload.get("payload", "")),
                )
            elif kind == "seen":
                seen.add(str(msg_id))
            elif kind == "ack":
                pending.pop(str(msg_id), None)
            else:
                raise RuntimeError(f"unknown record kind: {kind}")
        epoch = max_epoch + 1 if incarnations else 0
        return RecoveryState(
            epoch=epoch,
            physical_floor=floor,
            pending_sends=pending,
            seen_messages=seen,
            incarnation_count=incarnations,
        )

    def close(self) -> None:
        try:
            self._conn.close()
        finally:
            self._conn = None  # type: ignore[assignment]

    def remove_files(self) -> None:
        for suffix in ("", "-wal", "-shm"):
            try:
                os.remove(self.path + suffix)
            except FileNotFoundError:
                pass
