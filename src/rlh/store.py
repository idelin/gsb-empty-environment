"""Live device-state history: queries plus insert / retract / correct edits.

Every edit fully recomputes the runs from the raw records (a single,
easy-to-audit path), then diffs the new runs against the current segments so
that unchanged segments keep their stable ``sid`` and the caller receives an
incremental :class:`Patch`.
"""

from __future__ import annotations

from typing import Iterable, Optional

from .model import Patch, Record, RecordKey, Resolver, Segment
from .segmenter import Conflict, Run, build_runs, find_conflicts, source_order_key


class HistoryStore:
    def __init__(
        self,
        records: Iterable[Record] = (),
        resolver: Resolver | None = None,
        seed: int = 1,
    ) -> None:
        self._records: dict[tuple[str, int], Record] = {}
        self._resolver = resolver
        self._next_sid = seed
        self._segments: tuple[Segment, ...] = ()
        # Segments removed by earlier edits, pooled by content key so a
        # retract that restores prior structure reuses the original sid.
        self._pool: dict = {}
        for record in records:
            self._records[record.ref] = record
        self._segments = self._assign(build_runs(self._records.values(), self._resolver))

    def _assign(self, runs: Iterable[Run]) -> tuple[Segment, ...]:
        segments: list[Segment] = []
        for run in runs:
            segments.append(
                Segment(self._next_sid, run.state, run.start, run.end, run.sources)
            )
            self._next_sid += 1
        return tuple(segments)

    def _rebuild_and_diff(self, kind: str, touched: RecordKey) -> Patch:
        old_segments = list(self._segments)
        new_runs = build_runs(self._records.values(), self._resolver)
        new_by_key = {run.key: run for run in new_runs}

        kept: list[Segment] = []
        removed: list[Segment] = []
        used_new_keys: set = set()
        for segment in old_segments:
            if segment.key in new_by_key:
                kept.append(segment)
                used_new_keys.add(segment.key)
            else:
                removed.append(segment)
                self._pool.setdefault(segment.key, []).append(segment)

        added: list[Segment] = []
        for run in new_runs:
            if run.key in used_new_keys:
                continue
            reusable = self._pool.get(run.key)
            if reusable:
                added.append(reusable.pop())
                if not reusable:
                    del self._pool[run.key]
            else:
                added.append(
                    Segment(self._next_sid, run.state, run.start, run.end, run.sources)
                )
                self._next_sid += 1

        merged = sorted(kept + added, key=lambda seg: seg.start)
        self._segments = tuple(merged)
        return Patch(kind, touched, tuple(removed), tuple(added))

    # ------------------------------------------------------------------ edits

    def insert(self, record: Record) -> Patch:
        """Add a previously unseen record and return the segment delta."""
        if record.ref in self._records:
            raise ValueError(f"record {record.ref!r} already exists; use correct()")
        self._records[record.ref] = record
        return self._rebuild_and_diff("insert", RecordKey.of(record))

    def retract(self, source: str, seq: int) -> Patch:
        """Withdraw a record by identity and return the segment delta.

        The record disappears entirely (no tombstone), so retracting an insert
        restores the exact prior structure and segment ids.
        """
        ref = (source, seq)
        if ref not in self._records:
            raise ValueError(f"record {ref!r} does not exist")
        record = self._records.pop(ref)
        return self._rebuild_and_diff("retract", RecordKey.of(record))

    def correct(self, record: Record) -> Patch:
        """Replace the existing record with the same ``(source, seq)``.

        Implemented as an atomic retract+insert and emitted as one patch, so a
        correction can split, merge or shift without an intermediate visible
        state.
        """
        ref = record.ref
        if ref not in self._records:
            raise ValueError(f"record {ref!r} does not exist; use insert()")
        self._records[ref] = record
        return self._rebuild_and_diff("correct", RecordKey.of(record))

    # ---------------------------------------------------------------- queries

    def segments(self) -> tuple[Segment, ...]:
        """Current authoritative segments (direct query path)."""
        return self._segments

    def segment_at(self, t: int) -> Optional[Segment]:
        for segment in self._segments:
            if segment.contains(t):
                return segment
        return None

    def records(self) -> tuple[Record, ...]:
        return tuple(sorted(self._records.values(), key=source_order_key))

    def record(self, source: str, seq: int) -> Optional[Record]:
        return self._records.get((source, seq))

    def trace(self, sid: int) -> tuple[Record, ...]:
        """Return the raw records that formed segment ``sid``."""
        for segment in self._segments:
            if segment.sid == sid:
                return segment.sources
        raise KeyError(f"no live segment with id {sid}")

    def trace_at(self, t: int) -> tuple[Record, ...]:
        segment = self.segment_at(t)
        return segment.sources if segment is not None else ()

    def conflicts(self) -> tuple[Conflict, ...]:
        return find_conflicts(self._records.values(), self._resolver)

    def conflict_at(self, t: int) -> Optional[Conflict]:
        for conflict in self.conflicts():
            if conflict.time == t:
                return conflict
        return None
