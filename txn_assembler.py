"""Shard transaction assembler.

Collects change shards for a business transaction that spans multiple
table shards. A transaction is published atomically only when every
table listed in its manifest has arrived. Timeouts only mark the
transaction as pending; no partial commit ever happens.
"""

from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from enum import Enum
from typing import Any, Iterable


class TxnState(Enum):
    COLLECTING = "COLLECTING"
    PUBLISHED = "PUBLISHED"
    REJECTED = "REJECTED"
    PENDING_TIMEOUT = "PENDING_TIMEOUT"


def manifest_digest(required_tables: Iterable[str]) -> str:
    """Deterministic digest over the manifest's required table set."""
    canonical = json.dumps(sorted(required_tables), separators=(",", ":"))
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()[:16]


@dataclass(frozen=True)
class ChangeEvent:
    txn_id: str
    table: str
    primary_key: str
    before: Any
    after: Any
    commit_seq: int
    manifest_digest: str

    @classmethod
    def from_dict(cls, raw: dict) -> "ChangeEvent":
        return cls(
            txn_id=raw["txn_id"],
            table=raw["table"],
            primary_key=raw["primary_key"],
            before=raw.get("before"),
            after=raw.get("after"),
            commit_seq=int(raw["commit_seq"]),
            manifest_digest=raw["manifest_digest"],
        )


@dataclass(frozen=True)
class Manifest:
    txn_id: str
    required_tables: tuple[str, ...]
    deadline: float

    @property
    def digest(self) -> str:
        return manifest_digest(self.required_tables)

    @classmethod
    def from_dict(cls, raw: dict) -> "Manifest":
        return cls(
            txn_id=raw["txn_id"],
            required_tables=tuple(raw["required_tables"]),
            deadline=float(raw["deadline"]),
        )


class RejectReason(Enum):
    DIGEST_CONFLICT = "DIGEST_CONFLICT"
    UNKNOWN_TXN = "UNKNOWN_TXN"
    TABLE_NOT_IN_MANIFEST = "TABLE_NOT_IN_MANIFEST"


class TransactionAssembler:
    """Buffers shards per transaction and publishes on manifest completion."""

    def __init__(self, manifests: Iterable[Manifest]):
        self._manifests = {m.txn_id: m for m in manifests}
        self._buffers: dict[str, dict[str, ChangeEvent]] = {}
        self._states: dict[str, TxnState] = {}
        self._reject_reasons: dict[str, RejectReason] = {}
        self.published: list[list[ChangeEvent]] = []

    def state_of(self, txn_id: str) -> TxnState:
        return self._states.get(txn_id, TxnState.COLLECTING)

    def reject_reason_of(self, txn_id: str) -> RejectReason | None:
        return self._reject_reasons.get(txn_id)

    def buffered_tables(self, txn_id: str) -> set[str]:
        return set(self._buffers.get(txn_id, ()))

    def _reject(self, txn_id: str, reason: RejectReason) -> TxnState:
        self._states[txn_id] = TxnState.REJECTED
        self._reject_reasons[txn_id] = reason
        self._buffers.pop(txn_id, None)
        return TxnState.REJECTED

    def receive(self, event: ChangeEvent) -> TxnState:
        state = self.state_of(event.txn_id)
        if state is not TxnState.COLLECTING:
            return state

        manifest = self._manifests.get(event.txn_id)
        if manifest is None:
            return self._reject(event.txn_id, RejectReason.UNKNOWN_TXN)
        if event.manifest_digest != manifest.digest:
            return self._reject(event.txn_id, RejectReason.DIGEST_CONFLICT)
        if event.table not in manifest.required_tables:
            return self._reject(event.txn_id, RejectReason.TABLE_NOT_IN_MANIFEST)

        buffer = self._buffers.setdefault(event.txn_id, {})
        if event.table in buffer:
            # Duplicate shard: keep the first, does not count toward
            # any other missing table.
            return TxnState.COLLECTING
        buffer[event.table] = event

        if set(buffer) >= set(manifest.required_tables):
            return self._publish(event.txn_id)
        return TxnState.COLLECTING

    def _publish(self, txn_id: str) -> TxnState:
        buffer = self._buffers.pop(txn_id)
        ordered = sorted(buffer.values(), key=lambda e: e.commit_seq)
        self.published.append(ordered)
        self._states[txn_id] = TxnState.PUBLISHED
        return TxnState.PUBLISHED

    def expire_timeouts(self, now: float) -> list[str]:
        """Mark overdue incomplete transactions as pending.

        Buffered shards are kept for possible late completion, but
        nothing is published: no partial commit.
        """
        marked = []
        for txn_id, manifest in self._manifests.items():
            if self.state_of(txn_id) is TxnState.COLLECTING and now >= manifest.deadline:
                self._states[txn_id] = TxnState.PENDING_TIMEOUT
                marked.append(txn_id)
        return marked


def apply_to_store(store: dict[str, dict[str, Any]], events: Iterable[ChangeEvent]) -> None:
    """Apply a published transaction to an in-memory downstream store."""
    for event in events:
        table = store.setdefault(event.table, {})
        if event.after is None:
            table.pop(event.primary_key, None)
        else:
            table[event.primary_key] = event.after
