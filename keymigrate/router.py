"""Routes change events to the effective owner's partition.

Events already routed before a switchover stay in the old partition's store
and travel with the migration snapshot (nothing is lost). Events that arrive
*while* a key's transfer is in progress are buffered and delivered to the
winning owner once the transfer commits or rolls back.
"""
from __future__ import annotations

from collections import defaultdict
from typing import Optional

from .events import ChangeEvent, Key
from .registry import OwnershipRegistry
from .store import PartitionStore


class Router:
    def __init__(self, registry: OwnershipRegistry, stores: dict[int, PartitionStore]):
        self._registry = registry
        self._stores = stores
        self._buffers: dict[Key, list[ChangeEvent]] = defaultdict(list)
        # Routed log: (commit_seq, key, partition, owner version). Observability
        # + proof that pre-switchover deliveries are preserved.
        self.journal: list[tuple[int, Key, int, int]] = []

    def route(self, event: ChangeEvent) -> Optional[int]:
        key = event.key
        if self._registry.pending_transfer(key) is not None:
            self._buffers[key].append(event)
            return None
        record = self._registry.effective_owner(key)
        applied = self._stores[record.partition].apply(event, record.epoch)
        if applied:
            self.journal.append(
                (event.commit_seq, key, record.partition, record.version)
            )
        return record.partition

    def peek_buffer(self, key: Key) -> list[ChangeEvent]:
        return sorted(self._buffers.get(key, []), key=lambda e: e.commit_seq)

    def clear_buffer(self, key: Key) -> None:
        self._buffers.pop(key, None)

    def flush_buffer(self, key: Key) -> None:
        events = self.peek_buffer(key)
        self._buffers.pop(key, None)
        for event in events:
            self.route(event)
