"""Exact time model.

Three distinct concepts are modeled as separate types so they cannot be
mixed up accidentally:

- ``WallClock``: a reading on a wall clock (what a clock shows).  Two wall
  clock readings cannot be subtracted to obtain a physical duration,
  because the mapping between wall clock and physical time may be
  non-uniform (leap smears, offset changes, ...).
- ``Instant``: a point on the synthetic, monotonic time axis, represented
  exactly as an integer number of ticks.
- ``Duration``: a length of time on the synthetic axis, in ticks.

Conversion between the wall clock and the synthetic axis goes through an
``OffsetTable`` -- a synthetic offset translation table made of monotone
segments, each with an exact rational rate (ticks per wall unit).
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Union


Number = Union[int, Fraction]


@dataclass(frozen=True)
class Duration:
    """A length of time on the synthetic axis (exact, in ticks)."""

    ticks: int

    def __add__(self, other: "Duration") -> "Duration":
        if not isinstance(other, Duration):
            return NotImplemented
        return Duration(self.ticks + other.ticks)

    def __sub__(self, other: "Duration") -> "Duration":
        if not isinstance(other, Duration):
            return NotImplemented
        return Duration(self.ticks - other.ticks)

    def __neg__(self) -> "Duration":
        return Duration(-self.ticks)


@dataclass(frozen=True)
class Instant:
    """A point on the synthetic time axis (exact, in ticks)."""

    ticks: int

    def __sub__(self, other):
        if isinstance(other, Instant):
            return Duration(self.ticks - other.ticks)
        if isinstance(other, Duration):
            return Instant(self.ticks - other.ticks)
        return NotImplemented

    def __add__(self, other):
        if isinstance(other, Duration):
            return Instant(self.ticks + other.ticks)
        return NotImplemented

    def __lt__(self, other: "Instant") -> bool:
        return self.ticks < other.ticks

    def __le__(self, other: "Instant") -> bool:
        return self.ticks <= other.ticks


@dataclass(frozen=True)
class WallClockDelta:
    """Difference of two wall-clock readings.  NOT a physical duration."""

    span: Fraction


@dataclass(frozen=True)
class WallClock:
    """A wall-clock reading (exact rational number of wall units)."""

    reading: Fraction

    def __init__(self, reading: Number):
        object.__setattr__(self, "reading", Fraction(reading))

    def __sub__(self, other: "WallClock") -> WallClockDelta:
        if not isinstance(other, WallClock):
            return NotImplemented
        return WallClockDelta(self.reading - other.reading)


@dataclass(frozen=True)
class _Segment:
    wall_start: Fraction
    tick_start: int
    rate: Fraction  # synthetic ticks per wall unit, > 0


class OffsetTable:
    """Synthetic offset translation table.

    ``segments`` is an iterable of ``(wall_start, tick_start, rate)`` where
    ``wall_start`` is the first wall reading covered by the segment,
    ``tick_start`` the synthetic tick corresponding to it, and ``rate`` the
    exact rational number of ticks per wall unit.  Segments must be given
    in increasing wall order with positive rates; the resulting mapping is
    required to be strictly increasing in ticks as well.
    """

    def __init__(self, segments):
        segs = [
            _Segment(Fraction(w), int(t), Fraction(r)) for w, t, r in segments
        ]
        if not segs:
            raise ValueError("offset table needs at least one segment")
        for prev, cur in zip(segs, segs[1:]):
            if cur.wall_start <= prev.wall_start:
                raise ValueError("segments must be strictly increasing in wall time")
            if cur.rate <= 0:
                raise ValueError("rates must be positive")
            prev_end_tick = prev.tick_start + (
                cur.wall_start - prev.wall_start
            ) * prev.rate
            if Fraction(cur.tick_start) < prev_end_tick:
                raise ValueError("tick mapping must be strictly increasing")
        self._segments = tuple(segs)

    def _segment_for_wall(self, reading: Fraction) -> _Segment:
        seg = self._segments[0]
        if reading < seg.wall_start:
            raise ValueError("wall reading before start of offset table")
        for cand in self._segments[1:]:
            if reading < cand.wall_start:
                break
            seg = cand
        return seg

    def _segment_for_tick(self, tick: int) -> _Segment:
        seg = self._segments[0]
        if tick < seg.tick_start:
            raise ValueError("tick before start of offset table")
        for cand in self._segments[1:]:
            if tick < cand.tick_start:
                break
            seg = cand
        return seg

    def wall_to_ticks_exact(self, wall: WallClock) -> Fraction:
        """Exact (possibly fractional) synthetic tick position."""
        seg = self._segment_for_wall(wall.reading)
        return seg.tick_start + (wall.reading - seg.wall_start) * seg.rate

    def wall_to_instant(self, wall: WallClock) -> Instant:
        """Map a wall reading to an instant, flooring to whole ticks."""
        exact = self.wall_to_ticks_exact(wall)
        return Instant(exact.numerator // exact.denominator)

    def instant_to_wall(self, instant: Instant) -> WallClock:
        """Map an instant back to the exact wall reading."""
        seg = self._segment_for_tick(instant.ticks)
        reading = seg.wall_start + Fraction(instant.ticks - seg.tick_start, seg.rate)
        return WallClock(reading)

    def duration_between(self, start: WallClock, end: WallClock) -> Duration:
        """Physical duration between two wall readings (via the table)."""
        return self.wall_to_instant(end) - self.wall_to_instant(start)
