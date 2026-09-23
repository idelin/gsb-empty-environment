"""Coordinates shards of a business transaction and publishes all-or-nothing."""

from __future__ import annotations

import time
from dataclasses import dataclass, field
from enum import Enum
from typing import Callable

from .models import Manifest, Shard, TxState
from .store import TableStore


class ManifestConflictError(Exception):
    """Shard carries a manifest digest that conflicts with the registered one."""


class UnknownTransactionError(Exception):
    """Shard references a transaction with no registered manifest."""


class SubmitStatus(Enum):
    ACCEPTED = "accepted"                # shard stored, manifest not yet complete
    PUBLISHED = "published"              # shard completed the manifest; committed
    DUPLICATE = "duplicate"              # same table shard already held; ignored
    UNEXPECTED_TABLE = "unexpected_table"  # table not in the manifest; rejected
    ALREADY_COMMITTED = "already_committed"  # tx finished earlier; ignored


@dataclass(frozen=True)
class SubmitResult:
    status: SubmitStatus
    state: TxState
    missing: frozenset = frozenset()


@dataclass
class _TxEntry:
    manifest: Manifest
    registered_at: float
    state: TxState = TxState.COLLECTING
    shards: dict = field(default_factory=dict)  # table -> Shard


class ShardCoordinator:
    """Collects shards per manifest and commits only when the set is complete.

    Guarantees:
    - a transaction publishes only when every manifest table has exactly one
      accepted shard (duplicates never stand in for missing tables);
    - timeout only marks the transaction PENDING (undecided) and never
      publishes a partial set;
    - a shard whose manifest digest conflicts with the registered manifest is
      rejected.
    """

    def __init__(self, store: TableStore | None = None,
                 clock: Callable[[], float] = time.monotonic) -> None:
        self.store = store if store is not None else TableStore()
        self._clock = clock
        self._txs: dict[str, _TxEntry] = {}

    def register_manifest(self, manifest: Manifest) -> str:
        if manifest.tx_id in self._txs:
            raise ValueError(f"manifest already registered for {manifest.tx_id}")
        self._txs[manifest.tx_id] = _TxEntry(
            manifest=manifest, registered_at=self._clock()
        )
        return manifest.digest

    def submit_shard(self, shard: Shard) -> SubmitResult:
        entry = self._txs.get(shard.tx_id)
        if entry is None:
            raise UnknownTransactionError(shard.tx_id)
        if shard.manifest_digest != entry.manifest.digest:
            raise ManifestConflictError(
                f"tx {shard.tx_id}: shard digest {shard.manifest_digest} "
                f"!= manifest digest {entry.manifest.digest}"
            )
        if entry.state is TxState.COMMITTED:
            return SubmitResult(SubmitStatus.ALREADY_COMMITTED, TxState.COMMITTED)
        if shard.table not in entry.manifest.tables:
            return SubmitResult(
                SubmitStatus.UNEXPECTED_TABLE, entry.state,
                missing=self._missing(entry),
            )
        if shard.table in entry.shards:
            return SubmitResult(
                SubmitStatus.DUPLICATE, entry.state,
                missing=self._missing(entry),
            )

        entry.shards[shard.table] = shard
        if self._missing(entry):
            return SubmitResult(
                SubmitStatus.ACCEPTED, entry.state, missing=self._missing(entry)
            )
        self._publish(entry)
        return SubmitResult(SubmitStatus.PUBLISHED, TxState.COMMITTED)

    def sweep_timeouts(self) -> list[str]:
        """Mark expired, incomplete transactions PENDING. Never commits anything."""
        now = self._clock()
        expired = []
        for tx_id, entry in self._txs.items():
            if (
                entry.state is TxState.COLLECTING
                and now - entry.registered_at >= entry.manifest.ttl_seconds
            ):
                entry.state = TxState.PENDING
                expired.append(tx_id)
        return expired

    def state_of(self, tx_id: str) -> TxState:
        return self._txs[tx_id].state

    def missing_tables(self, tx_id: str) -> frozenset:
        return self._missing(self._txs[tx_id])

    @staticmethod
    def _missing(entry: _TxEntry) -> frozenset:
        return frozenset(entry.manifest.tables - entry.shards.keys())

    def _publish(self, entry: _TxEntry) -> None:
        changes = [
            change
            for shard in entry.shards.values()
            for change in shard.changes
        ]
        changes.sort(key=lambda change: change.commit_seq)
        self.store.apply_all(changes)  # atomic: all or nothing
        entry.state = TxState.COMMITTED
