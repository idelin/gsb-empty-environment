"""Per-partition state store with idempotent, fence-checked applies."""
from __future__ import annotations

from typing import Optional

from .events import ChangeEvent, Key
from .registry import OwnershipRegistry


class FencingError(Exception):
    """A write carried a stale/invalid epoch (not the effective owner)."""


class PartitionStore:
    def __init__(self, partition_id: int, registry: OwnershipRegistry):
        self.partition_id = partition_id
        self._registry = registry
        self.data: dict[Key, dict] = {}
        self.watermarks: dict[Key, int] = {}  # key -> last applied commit_seq

    def apply(self, event: ChangeEvent, epoch: int) -> bool:
        if not self._registry.validate_write(event.key, self.partition_id, epoch):
            raise FencingError(
                f"stale epoch {epoch} for {event.key} on partition {self.partition_id}"
            )
        return self.apply_idempotent(event)

    def apply_idempotent(self, event: ChangeEvent) -> bool:
        """Dedupe by per-key commit-seq watermark; safe to replay."""
        last = self.watermarks.get(event.key, -1)
        if event.commit_seq <= last:
            return False
        if event.after is None:
            self.data.pop(event.key, None)
        else:
            self.data[event.key] = dict(event.after)
        self.watermarks[event.key] = event.commit_seq
        return True

    def stage(self, key: Key, value: dict, watermark: int) -> None:
        """Copy a snapshot during migration; never duplicates state."""
        self.data.setdefault(key, dict(value))
        self.watermarks[key] = max(self.watermarks.get(key, -1), watermark)

    def discard(self, key: Key) -> None:
        self.data.pop(key, None)
        self.watermarks.pop(key, None)

    def export_key(self, key: Key) -> tuple[Optional[dict], int]:
        value = self.data.get(key)
        return (dict(value) if value is not None else None,
                self.watermarks.get(key, -1))

    def remove(self, key: Key) -> None:
        self.discard(key)
