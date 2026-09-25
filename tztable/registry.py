"""Versioned table registry with hot reload.

Publishing a table binds it to its ``version`` forever: snapshots retrieved
by version never change, so historical conversions are stable.  Publishing a
new version makes it current for future lookups; cached conversion results
are keyed by version, so a hot update causes *local* invalidation (only
entries for the new version are (re)computed) without disturbing results
already obtained from older snapshots.
"""

from __future__ import annotations

from .core import WallClock
from .table import Candidate, TransitionTable


class TableRegistry:
    """Holds immutable table snapshots keyed by version."""

    def __init__(self) -> None:
        self._tables: dict[str, TransitionTable] = {}
        self._current: str | None = None

    def publish(self, table: TransitionTable) -> str:
        """Hot-update: make ``table`` current without touching old versions."""
        if table.version in self._tables:
            raise ValueError(f"version already published: {table.version}")
        self._tables[table.version] = table
        self._current = table.version
        return table.version

    @property
    def current_version(self) -> str:
        if self._current is None:
            raise LookupError("no table published yet")
        return self._current

    def snapshot(self, version: str | None = None) -> TransitionTable:
        """Return the immutable snapshot for ``version`` (default: current)."""
        key = self.current_version if version is None else version
        try:
            return self._tables[key]
        except KeyError:
            raise LookupError(f"unknown table version: {key}") from None

    @property
    def versions(self) -> tuple[str, ...]:
        return tuple(self._tables)


class ConversionService:
    """Wall-to-instant conversion with per-version caching.

    Results are cached under ``(version, wall)``.  After a hot update the
    service resolves against the new current version, so stale entries are
    never served for it (local invalidation); explicitly pinned older
    versions still answer from their immutable snapshots.
    """

    def __init__(self, registry: TableRegistry) -> None:
        self._registry = registry
        self._cache: dict[tuple[str, WallClock], tuple[Candidate, ...]] = {}

    def wall_to_instants(
        self, wall: WallClock, *, version: str | None = None
    ) -> tuple[Candidate, ...]:
        key_version = self._registry.current_version if version is None else version
        cache_key = (key_version, wall)
        if cache_key not in self._cache:
            self._cache[cache_key] = self._registry.snapshot(key_version).wall_to_instants(wall)
        return self._cache[cache_key]

    def invalidate(self, version: str | None = None) -> None:
        """Drop cached entries for one version, or everything if omitted."""
        if version is None:
            self._cache.clear()
        else:
            for key in [k for k in self._cache if k[0] == version]:
                del self._cache[key]

    @property
    def cache_size(self) -> int:
        return len(self._cache)
