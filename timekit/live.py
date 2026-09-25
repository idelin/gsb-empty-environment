from __future__ import annotations

from dataclasses import dataclass

from .convert import (
    LocalIntervalMapping,
    map_instant,
    map_local_interval,
    map_utc_interval,
    resolve_local_point,
)
from .model import Instant, InstantInterval, WallClock, WallInterval
from .table import (
    TableVersion,
    Transition,
    TransitionTable,
    wall_at,
)


@dataclass(frozen=True, slots=True)
class CachedIntervalEntry:
    key: WallInterval
    candidates: tuple[object, ...]
    utc_union: tuple[InstantInterval, ...]
    covered_wall_union: tuple[WallInterval, ...]
    missing_wall_parts: tuple[object, ...]


class MappingCache:
    def __init__(self) -> None:
        self._entries: dict[WallInterval, CachedIntervalEntry] = {}
        self.computations = 0
        self.invalidations = 0

    def get_or_compute(
        self, interval: WallInterval, table: TransitionTable
    ) -> LocalIntervalMapping:
        entry = self._entries.get(interval)
        if entry is None:
            self.computations += 1
            mapping = map_local_interval(interval, table)
            self._entries[interval] = CachedIntervalEntry(
                interval,
                mapping.candidates,
                mapping.utc_union,
                mapping.covered_wall_union,
                mapping.missing_wall_parts,
            )
            return mapping
        return self._rebuild(entry, table.version)

    def invalidate_span(self, start: WallClock, end: WallClock) -> None:
        keys = [
            key
            for key in self._entries
            if key.start < end and start < key.end
        ]
        for key in keys:
            del self._entries[key]
        self.invalidations += len(keys)

    def invalidate_from(self, start: WallClock) -> None:
        keys = {key for key in self._entries if key.end > start}
        for key in keys:
            del self._entries[key]
        self.invalidations += len(keys)

    def invalidate_spans(
        self, spans: list[tuple[WallClock, WallClock | None]]
    ) -> None:
        keys = set()
        for start, end in spans:
            if end is None:
                keys.update(key for key in self._entries if key.end > start)
            else:
                keys.update(
                    key
                    for key in self._entries
                    if key.start < end and start < key.end
                )
        for key in keys:
            del self._entries[key]
        self.invalidations += len(keys)

    def clear(self) -> None:
        self.invalidations += len(self._entries)
        self._entries.clear()

    def __len__(self) -> int:
        return len(self._entries)

    @staticmethod
    def _rebuild(
        entry: CachedIntervalEntry, version: TableVersion
    ) -> LocalIntervalMapping:
        return LocalIntervalMapping(
            entry.key,
            entry.candidates,  # type: ignore[arg-type]
            entry.utc_union,
            entry.covered_wall_union,
            entry.missing_wall_parts,  # type: ignore[arg-type]
            version,
        )


class LiveTransitionTable:
    def __init__(
        self,
        table: TransitionTable,
    ) -> None:
        self._current = table
        self._snapshots: dict[TableVersion, TransitionTable] = {table.version: table}
        self.cache = MappingCache()

    @property
    def current(self) -> TransitionTable:
        return self._current

    def snapshot(self, version: TableVersion | None = None) -> TransitionTable:
        if version is None:
            return self._current
        return self._snapshots[version]

    def versions(self) -> tuple[TableVersion, ...]:
        return tuple(self._snapshots)

    def update(
        self,
        transitions: tuple[Transition, ...] | list[Transition],
        *,
        initial_offset: object | None = None,
        version: TableVersion | None = None,
    ) -> TransitionTable:
        old = self._current
        next_version = self._next_version(version)
        updated = TransitionTable(
            old.initial_offset if initial_offset is None else initial_offset,  # type: ignore[arg-type]
            tuple(transitions),
            next_version,
        )
        self._invalidate(old, updated)
        self._current = updated
        self._snapshots[next_version] = updated
        return updated

    def map_instant(self, instant: Instant) -> WallClock:
        return map_instant(instant, self._current)

    def map_utc_interval(self, interval: InstantInterval) -> object:
        return map_utc_interval(interval, self._current)

    def resolve_local_point(self, wall: WallClock, policy: str = "earlier") -> Instant:
        return resolve_local_point(wall, self._current, policy)  # type: ignore[arg-type]

    def map_local_interval(self, interval: WallInterval) -> LocalIntervalMapping:
        return self.cache.get_or_compute(interval, self._current)

    def _next_version(self, requested: TableVersion | None) -> TableVersion:
        if requested is not None:
            if requested in self._snapshots:
                raise ValueError("table version already exists")
            return requested
        integer_versions = [value for value in self._snapshots if isinstance(value, int)]
        return (max(integer_versions) + 1) if integer_versions else 1

    def _invalidate(
        self, old: TransitionTable, updated: TransitionTable
    ) -> None:
        if old.initial_offset != updated.initial_offset:
            self.cache.clear()
            return

        old_by_id = {item.transition_id: item for item in old.transitions}
        new_by_id = {item.transition_id: item for item in updated.transitions}
        changed_ids = set(old_by_id) | set(new_by_id)
        old_previous = self._preceding_offsets(old)
        new_previous = self._preceding_offsets(updated)

        for transition_id in set(old_by_id) | set(new_by_id):
            spans: list[tuple[WallClock, WallClock | None]] = []
            old_transition = old_by_id.get(transition_id)
            new_transition = new_by_id.get(transition_id)
            if old_transition is None and new_transition is not None:
                span = self._transition_span(
                    new_transition, updated.initial_offset, new_previous
                )
                spans.extend((span, (span[0], None)))
            elif new_transition is None and old_transition is not None:
                span = self._transition_span(
                    old_transition, old.initial_offset, old_previous
                )
                spans.extend((span, (span[0], None)))
            elif old_transition is not None and new_transition is not None:
                old_span = self._transition_span(
                    old_transition, old.initial_offset, old_previous
                )
                new_span = self._transition_span(
                    new_transition, updated.initial_offset, new_previous
                )
                spans.extend((old_span, new_span))
                if old_transition.offset_after != new_transition.offset_after:
                    spans.append((min(old_span[0], new_span[0]), None))
            self.cache.invalidate_spans(spans)

    def _transition_span(
        self,
        transition: Transition,
        initial_offset: object,
        previous_offsets: dict[str, object],
    ) -> tuple[WallClock, WallClock | None]:
        before_offset = previous_offsets.get(transition.transition_id, initial_offset)
        before_wall = wall_at(transition.at, before_offset)  # type: ignore[arg-type]
        after_wall = wall_at(transition.at, transition.offset_after)
        start = min(
            before_wall,
            after_wall,
        )
        end = max(before_wall, after_wall)
        return start, end

    @staticmethod
    def _preceding_offsets(table: TransitionTable) -> dict[str, object]:
        offsets = {}
        previous = table.initial_offset
        for transition in table.transitions:
            offsets[transition.transition_id] = previous
            previous = transition.offset_after
        return offsets
