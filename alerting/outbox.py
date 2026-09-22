"""Confirmable notification outbox.

Every notification event is first *recorded* (PENDING), then *sent*
(SENT), then *confirmed* (ACKED).  Entries are idempotent by
``dedupe_key = alert_id|kind|revision`` so recovery after a crash never
duplicates a notification and never loses a correction.  The outbox is
append-only: revocations are new entries, not deletions.
"""

from __future__ import annotations

import enum
import json
import os
from dataclasses import asdict, dataclass, field

from .models import NotificationKind


class OutboxStatus(str, enum.Enum):
    PENDING = "PENDING"  # recorded, not yet handed to the transport
    SENT = "SENT"        # handed to the transport, not yet confirmed
    ACKED = "ACKED"      # confirmed by the receiver


class TransportError(Exception):
    pass


@dataclass
class OutboxEntry:
    entry_id: str
    dedupe_key: str
    alert_id: str
    kind: NotificationKind
    revision: int
    payload: dict = field(default_factory=dict)
    status: OutboxStatus = OutboxStatus.PENDING
    attempts: int = 0


class RecordingTransport:
    """Test transport with receiver-side idempotency and failure injection."""

    def __init__(self, fail_keys: set[str] | None = None):
        self.fail_keys = set(fail_keys or ())
        self.attempts: list[str] = []   # every send attempt (idempotency keys)
        self.delivered: list[str] = []  # accepted by the receiver (deduped)
        self.duplicates = 0

    def send(self, idempotency_key: str, entry: OutboxEntry) -> None:
        if idempotency_key in self.fail_keys:
            raise TransportError(f"injected failure for {idempotency_key}")
        self.attempts.append(idempotency_key)
        if idempotency_key in self.delivered:
            self.duplicates += 1
        else:
            self.delivered.append(idempotency_key)


class Outbox:
    def __init__(self, path: str | None = None):
        self.path = path
        self.entries: list[OutboxEntry] = []
        self._by_key: dict[str, OutboxEntry] = {}
        self._seq = 0
        if path and os.path.exists(path):
            self._load()

    # -- recording ------------------------------------------------------
    def enqueue(
        self,
        alert_id: str,
        kind: NotificationKind,
        revision: int,
        payload: dict | None = None,
    ) -> tuple[OutboxEntry, bool]:
        """Record an event.  Returns (entry, created); dedupes by key."""
        key = f"{alert_id}|{kind.value}|{revision}"
        existing = self._by_key.get(key)
        if existing is not None:
            return existing, False
        self._seq += 1
        entry = OutboxEntry(
            entry_id=f"ob-{self._seq}",
            dedupe_key=key,
            alert_id=alert_id,
            kind=kind,
            revision=revision,
            payload=payload or {},
        )
        self.entries.append(entry)
        self._by_key[key] = entry
        self._persist()
        return entry, True

    # -- delivery -------------------------------------------------------
    def dispatch(self, transport: RecordingTransport) -> None:
        """Send PENDING entries in order.  SENT entries are never resent;
        after a crash the receiver's idempotency key still protects us."""
        for entry in self.entries:
            if entry.status != OutboxStatus.PENDING:
                continue
            try:
                transport.send(entry.dedupe_key, entry)
            except TransportError:
                break  # preserve ordering; retry later
            entry.status = OutboxStatus.SENT
            entry.attempts += 1
            self._persist()

    def confirm(self, entry_id: str) -> OutboxEntry:
        for entry in self.entries:
            if entry.entry_id == entry_id:
                if entry.status != OutboxStatus.SENT:
                    raise ValueError(f"cannot confirm entry in state {entry.status}")
                entry.status = OutboxStatus.ACKED
                self._persist()
                return entry
        raise KeyError(entry_id)

    def unacked(self) -> list[OutboxEntry]:
        return [e for e in self.entries if e.status != OutboxStatus.ACKED]

    # -- persistence ----------------------------------------------------
    def _persist(self) -> None:
        if not self.path:
            return
        tmp = self.path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as fh:
            json.dump(
                {
                    "seq": self._seq,
                    "entries": [
                        {**asdict(e), "kind": e.kind.value, "status": e.status.value}
                        for e in self.entries
                    ],
                },
                fh,
                indent=2,
            )
        os.replace(tmp, self.path)

    def _load(self) -> None:
        with open(self.path, encoding="utf-8") as fh:
            data = json.load(fh)
        self._seq = data["seq"]
        for raw in data["entries"]:
            entry = OutboxEntry(
                entry_id=raw["entry_id"],
                dedupe_key=raw["dedupe_key"],
                alert_id=raw["alert_id"],
                kind=NotificationKind(raw["kind"]),
                revision=raw["revision"],
                payload=raw["payload"],
                status=OutboxStatus(raw["status"]),
                attempts=raw["attempts"],
            )
            self.entries.append(entry)
            self._by_key[entry.dedupe_key] = entry
