"""Ownership registry: exactly one valid owner per entity key."""
from __future__ import annotations

from dataclasses import dataclass, field

from .events import ChangeEvent, Key


@dataclass
class OwnerRecord:
    owner: str
    epoch: int
    version: int  # partition-function version that assigned this owner


@dataclass
class PendingTransfer:
    migration_id: str
    key: Key
    from_owner: str
    to_owner: str
    new_epoch: int
    new_version: int
    # Old-route events captured while the transfer is pending, so they can be
    # forwarded to the new owner on commit (never lost during the switch).
    inflight: list[ChangeEvent] = field(default_factory=list)


class OwnershipRegistry:
    """Single source of truth for key ownership.

    A key has exactly one OwnerRecord at all times; a pending transfer does
    not change the effective owner until commit, and rollback leaves the
    record untouched, so there is never zero or dual ownership.
    """

    def __init__(self):
        self.records: dict[Key, OwnerRecord] = {}
        self.pending: dict[Key, PendingTransfer] = {}
        self.active_migrations: set[str] = set()
        self.completed_migrations: set[str] = set()

    def ensure_key(self, key: Key, owner: str, version: int) -> OwnerRecord:
        rec = self.records.get(key)
        if rec is None:
            rec = OwnerRecord(owner=owner, epoch=0, version=version)
            self.records[key] = rec
        return rec

    def owner_of(self, key: Key) -> tuple[str, int] | None:
        rec = self.records.get(key)
        return (rec.owner, rec.epoch) if rec else None

    def begin_transfer(
        self, migration_id: str, key: Key, to_owner: str, new_version: int
    ) -> PendingTransfer:
        if key in self.pending:
            raise ValueError(f"transfer already pending for {key}")
        rec = self.records[key]
        if rec.owner == to_owner:
            raise ValueError(f"no-op transfer for {key}")
        pt = PendingTransfer(
            migration_id=migration_id,
            key=key,
            from_owner=rec.owner,
            to_owner=to_owner,
            new_epoch=rec.epoch + 1,
            new_version=new_version,
        )
        self.pending[key] = pt
        return pt

    def commit_transfer(self, migration_id: str, key: Key) -> PendingTransfer:
        pt = self.pending.pop(key)
        if pt.migration_id != migration_id:
            raise ValueError(f"migration id mismatch for {key}")
        self.records[key] = OwnerRecord(
            owner=pt.to_owner, epoch=pt.new_epoch, version=pt.new_version
        )
        return pt

    def rollback_transfer(self, migration_id: str, key: Key) -> PendingTransfer:
        pt = self.pending.pop(key)
        if pt.migration_id != migration_id:
            raise ValueError(f"migration id mismatch for {key}")
        # Owner record untouched: the old owner remains the only valid owner.
        return pt
