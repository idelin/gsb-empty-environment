"""Synthetic offset conversion table between wall-clock and logical time.

Each entry converts exactly:  logical = logical_start + (real - real_start) * rate.
The table is a compact view over a PiecewiseMap and keeps the source map for
pre-image queries.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .intervals import Interval, IntervalSet
from .mapping import Evidence, PiecewiseMap
from .types import LogicalTime, WallClock, exact


@dataclass(frozen=True)
class TableEntry:
    real_start: Fraction
    real_end: Fraction
    logical_start: Fraction
    rate: Fraction
    end_open: bool = True
    evidence: tuple[Evidence, ...] = ()

    def to_logical(self, t) -> Fraction:
        return self.logical_start + (exact(t) - self.real_start) * self.rate


class OffsetTable:
    def __init__(self, entries, source_map: PiecewiseMap | None = None):
        self._entries = tuple(entries)
        self._map = source_map

    @classmethod
    def from_map(cls, m: PiecewiseMap) -> "OffsetTable":
        return cls(
            [TableEntry(s.start, s.end, s.value_at(s.start), s.slope,
                        s.end_open, s.evidence)
             for s in m.segments],
            source_map=m,
        )

    @property
    def entries(self) -> tuple[TableEntry, ...]:
        return self._entries

    def entry_at(self, w: WallClock) -> TableEntry:
        t = w.value
        for i, e in enumerate(self._entries):
            if e.real_start <= t < e.real_end:
                return e
            if i == len(self._entries) - 1 and t == e.real_end:
                return e
        raise KeyError(f"{w} is outside the table range")

    def to_logical(self, w: WallClock) -> LogicalTime:
        return LogicalTime(self.entry_at(w).to_logical(w.value))

    def to_real(self, lt: LogicalTime) -> IntervalSet:
        """All wall-clock choices mapping to a logical point (needs source map)."""
        if self._map is None:
            raise ValueError("this table has no source map for pre-image queries")
        return self._map.preimage(Interval.point(lt.value))
