"""Synthetic per-source offset conversion table.

Each source clock is related to the reference timeline by a fixed exact
offset: reference = reading + offset[source]. Offsets are synthetic (given,
not estimated) and exact (Fraction).
"""

from __future__ import annotations

from fractions import Fraction

from .timevals import Duration, TimePoint, WallClock


class OffsetTable:
    def __init__(self, offsets: dict):
        self._offsets = {
            source: (value if isinstance(value, Fraction) else Fraction(value))
            for source, value in offsets.items()
        }

    def offset(self, source: str) -> Duration:
        return Duration(self._offsets[source])

    def to_reference(self, clock: WallClock) -> TimePoint:
        if clock.source not in self._offsets:
            raise KeyError(f"no offset registered for source {clock.source!r}")
        return TimePoint(self._offsets[clock.source] + clock.reading)

    def sources(self):
        return tuple(sorted(self._offsets))
