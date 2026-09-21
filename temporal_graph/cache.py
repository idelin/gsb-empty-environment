from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from .graph import ChangeEvent, TemporalGraph


@dataclass(frozen=True)
class CacheEntry:
    namespace: str
    key: tuple[Any, ...]
    snapshot_id: int
    dependencies: frozenset[tuple[str, str, int]]
    value: Any
    revoked_at: int | None = None

    @property
    def active(self) -> bool:
        return self.revoked_at is None


@dataclass
class CacheStats:
    hits: int = 0
    misses: int = 0
    stores: int = 0
    revocations: int = 0


class EvidenceCache:
    def __init__(self, graph: TemporalGraph) -> None:
        self._entries: list[CacheEntry] = []
        self.stats = CacheStats()
        graph.add_listener(self._on_change)

    def get(
        self,
        namespace: str,
        key: tuple[Any, ...],
        snapshot_id: int,
        dependencies: dict[tuple[str, str], int] | None = None,
    ) -> CacheEntry | None:
        required = dependencies or {}
        for entry in reversed(self._entries):
            if entry.namespace != namespace or entry.key != key:
                continue
            if entry.revoked_at is not None and snapshot_id >= entry.revoked_at:
                continue
            if not self._dependencies_match(entry.dependencies, required):
                continue
            if entry.snapshot_id > snapshot_id:
                continue
            self.stats.hits += 1
            return entry
        self.stats.misses += 1
        return None

    def put(
        self,
        namespace: str,
        key: tuple[Any, ...],
        snapshot_id: int,
        dependencies: dict[tuple[str, str], int],
        value: Any,
    ) -> CacheEntry:
        normalized = frozenset(
            (kind, record_id, version)
            for (kind, record_id), version in dependencies.items()
        )
        entry = CacheEntry(namespace, key, snapshot_id, normalized, value)
        self._entries.append(entry)
        self.stats.stores += 1
        return entry

    def entries(self, include_revoked: bool = True) -> tuple[CacheEntry, ...]:
        entries = tuple(self._entries)
        if include_revoked:
            return entries
        return tuple(entry for entry in entries if entry.active)

    def active_count(self) -> int:
        return sum(1 for entry in self._entries if entry.active)

    def _on_change(self, event: ChangeEvent) -> None:
        if event.kind not in {"edge", "vertex"}:
            return
        for entry in self._entries:
            if not entry.active:
                continue
            dependency = (event.kind, event.record_id, event.old_version)
            if dependency in entry.dependencies:
                object.__setattr__(entry, "revoked_at", event.revision)
                self.stats.revocations += 1

    @staticmethod
    def _dependencies_match(
        stored: frozenset[tuple[str, str, int]],
        required: dict[tuple[str, str], int],
    ) -> bool:
        normalized_required = frozenset(
            (kind, record_id, version)
            for (kind, record_id), version in required.items()
        )
        return normalized_required.issubset(stored)
