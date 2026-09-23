"""Ownership registry.

Invariant: every key has exactly one *effective* owner (partition, version,
epoch). Ownership changes only through begin/commit/rollback of a transfer.
Epochs act as fencing tokens: a store accepts a write only when the presented
epoch matches the current effective owner (or the pending new owner while a
transfer is being staged).
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Optional

from .events import Key
from .partition import PartitionFunction


class OwnershipError(Exception):
    pass


@dataclass(frozen=True)
class OwnerRecord:
    partition: int
    version: int
    epoch: int


@dataclass
class PendingTransfer:
    key: Key
    old: OwnerRecord
    new_partition: int
    new_version: int
    new_epoch: int
    cutover_seq: int  # last commit_seq included in the state snapshot


class OwnershipRegistry:
    def __init__(self, partition_function: PartitionFunction):
        self._pf = partition_function
        self._owners: dict[Key, OwnerRecord] = {}
        self._pending: dict[Key, PendingTransfer] = {}
        self._epoch = 0

    @property
    def partition_function(self) -> PartitionFunction:
        return self._pf

    def update_partition_function(self, pf: PartitionFunction) -> None:
        self._pf = pf

    def _next_epoch(self) -> int:
        self._epoch += 1
        return self._epoch

    def known_keys(self) -> list[Key]:
        return sorted(self._owners)

    def effective_owner(self, key: Key) -> OwnerRecord:
        record = self._owners.get(key)
        if record is None:
            record = OwnerRecord(
                partition=self._pf.partition_for(*key),
                version=self._pf.version,
                epoch=self._next_epoch(),
            )
            self._owners[key] = record
        return record

    def begin_transfer(
        self, key: Key, new_partition: int, new_version: int, cutover_seq: int
    ) -> PendingTransfer:
        if key in self._pending:
            raise OwnershipError(f"transfer already in progress for {key}")
        old = self.effective_owner(key)
        transfer = PendingTransfer(
            key=key,
            old=old,
            new_partition=new_partition,
            new_version=new_version,
            new_epoch=self._next_epoch(),
            cutover_seq=cutover_seq,
        )
        self._pending[key] = transfer
        return transfer

    def commit_transfer(self, key: Key) -> OwnerRecord:
        transfer = self._pending.pop(key, None)
        if transfer is None:
            raise OwnershipError(f"no pending transfer for {key}")
        record = OwnerRecord(
            partition=transfer.new_partition,
            version=transfer.new_version,
            epoch=transfer.new_epoch,
        )
        self._owners[key] = record
        return record

    def rollback_transfer(self, key: Key) -> OwnerRecord:
        transfer = self._pending.pop(key, None)
        if transfer is None:
            raise OwnershipError(f"no pending transfer for {key}")
        self._owners[key] = transfer.old
        return transfer.old

    def pending_transfer(self, key: Key) -> Optional[PendingTransfer]:
        return self._pending.get(key)

    def validate_write(self, key: Key, partition_id: int, epoch: int) -> bool:
        transfer = self._pending.get(key)
        if transfer is not None:
            # While staging, only the pending new owner may be written.
            return partition_id == transfer.new_partition and epoch == transfer.new_epoch
        record = self._owners.get(key)
        return (
            record is not None
            and record.partition == partition_id
            and record.epoch == epoch
        )
