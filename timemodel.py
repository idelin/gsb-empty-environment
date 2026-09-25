"""Exact time model: wall-clock instants, synthetic time points, durations.

Three distinct concepts are kept as separate types so they cannot be mixed
up by accident:

- ``WallClock``: an instant on the wall-clock calendar, stored as an exact
  ``Fraction`` of seconds.
- ``TimePoint``: an instant on the synthetic (engine-internal) timeline,
  stored as an exact integer number of ticks.
- ``Duration``: a length of time on the synthetic timeline, in ticks.

``OffsetTable`` converts between wall-clock instants and synthetic ticks via
a piecewise table of (wall start, tick start, rate) segments. All arithmetic
is exact (``Fraction`` / ``int``); conversions that would land between ticks
raise ``ValueError`` instead of silently rounding.
"""

from __future__ import annotations

from bisect import bisect_right
from dataclasses import dataclass
from fractions import Fraction
from typing import Iterable, Tuple


@dataclass(frozen=True, order=True)
class Duration:
    """A length of time on the synthetic timeline, in exact ticks."""

    ticks: int

    def __add__(self, other: "Duration") -> "Duration":
        if not isinstance(other, Duration):
            return NotImplemented
        return Duration(self.ticks + other.ticks)

    def __sub__(self, other: "Duration") -> "Duration":
        if not isinstance(other, Duration):
            return NotImplemented
        return Duration(self.ticks - other.ticks)

    def __mul__(self, factor: int) -> "Duration":
        if not isinstance(factor, int):
            return NotImplemented
        return Duration(self.ticks * factor)


@dataclass(frozen=True, order=True)
class TimePoint:
    """An instant on the synthetic timeline, in exact integer ticks."""

    ticks: int

    def __add__(self, duration: Duration) -> "TimePoint":
        if not isinstance(duration, Duration):
            return NotImplemented
        return TimePoint(self.ticks + duration.ticks)

    def __sub__(self, other):
        if isinstance(other, Duration):
            return TimePoint(self.ticks - other.ticks)
        if isinstance(other, TimePoint):
            return Duration(self.ticks - other.ticks)
        return NotImplemented


@dataclass(frozen=True, order=True)
class WallClock:
    """A wall-clock instant, stored as an exact Fraction of seconds."""

    seconds: Fraction

    def __init__(self, seconds) -> None:
        object.__setattr__(self, "seconds", Fraction(seconds))


class OffsetTable:
    """Piecewise wall-clock <-> synthetic-tick conversion table.

    Each segment is ``(wall_start_seconds, tick_start, rate_ticks_per_sec)``.
    Within a segment: ``tick = tick_start + (wall - wall_start) * rate``.
    Segments must be sorted by wall start and by tick start.
    """

    def __init__(self, segments: Iterable[Tuple[Fraction, int, Fraction]]) -> None:
        rows = [(Fraction(w), int(t), Fraction(r)) for w, t, r in segments]
        if not rows:
            raise ValueError("offset table needs at least one segment")
        walls = [w for w, _, _ in rows]
        ticks = [t for _, t, _ in rows]
        if walls != sorted(walls) or ticks != sorted(ticks):
            raise ValueError("segments must be sorted by wall start and tick start")
        self._rows = tuple(rows)
        self._walls = tuple(walls)
        self._ticks = tuple(ticks)

    def wall_to_point(self, wall: WallClock) -> TimePoint:
        idx = bisect_right(self._walls, wall.seconds) - 1
        if idx < 0:
            raise ValueError(f"wall clock {wall.seconds}s predates the table")
        wall_start, tick_start, rate = self._rows[idx]
        ticks = tick_start + (wall.seconds - wall_start) * rate
        if ticks.denominator != 1:
            raise ValueError(f"wall clock {wall.seconds}s is not on a tick boundary")
        return TimePoint(int(ticks))

    def point_to_wall(self, point: TimePoint) -> WallClock:
        idx = bisect_right(self._ticks, point.ticks) - 1
        if idx < 0:
            raise ValueError(f"tick {point.ticks} predates the table")
        wall_start, tick_start, rate = self._rows[idx]
        return WallClock(wall_start + Fraction(point.ticks - tick_start, rate))
