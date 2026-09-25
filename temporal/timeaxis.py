"""Exact time primitives.

Three distinct concepts are modelled and are NOT interchangeable:

* ``WallClock`` -- a wall-clock reading (civil time, tz-aware datetime).
* ``TimePoint`` -- an exact instant on the synthetic valid-time axis,
  expressed as an exact ``Fraction`` of ticks since the axis origin.
* ``Duration``  -- an exact length of time, also in ticks.

An ``OffsetTable`` is a synthetic, piecewise-linear conversion table between
wall-clock readings and time points.  All arithmetic is exact (Fraction
based); nothing is ever rounded to floats.
"""
from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timedelta
from fractions import Fraction
from typing import Iterable, Tuple

_MICROS_PER_SECOND = 1_000_000


@dataclass(frozen=True, order=True)
class Duration:
    """An exact length of time, in ticks."""

    ticks: Fraction

    def __post_init__(self) -> None:
        object.__setattr__(self, "ticks", Fraction(self.ticks))

    @classmethod
    def seconds(cls, value) -> "Duration":
        """A duration of ``value`` seconds at one tick per second."""
        return cls(Fraction(value))

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self.ticks + other.ticks)
        return NotImplemented

    __radd__ = __add__

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self.ticks - other.ticks)
        return NotImplemented

    def __mul__(self, factor):
        return Duration(self.ticks * Fraction(factor))

    __rmul__ = __mul__

    def __neg__(self):
        return Duration(-self.ticks)


@dataclass(frozen=True, order=True)
class TimePoint:
    """An exact instant on the synthetic axis (ticks since the origin)."""

    ticks: Fraction

    def __post_init__(self) -> None:
        object.__setattr__(self, "ticks", Fraction(self.ticks))

    def __add__(self, other):
        if isinstance(other, Duration):
            return TimePoint(self.ticks + other.ticks)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, TimePoint):
            return Duration(self.ticks - other.ticks)
        if isinstance(other, Duration):
            return TimePoint(self.ticks - other.ticks)
        return NotImplemented


@dataclass(frozen=True, order=True)
class WallClock:
    """A wall-clock reading.  Deliberately a different type from TimePoint:
    mixing the two in comparisons or arithmetic raises TypeError."""

    at: datetime

    def __post_init__(self) -> None:
        if self.at.tzinfo is None or self.at.utcoffset() is None:
            raise ValueError("WallClock requires a timezone-aware datetime")


@dataclass(frozen=True)
class _Anchor:
    wall: WallClock
    point: TimePoint
    rate: Fraction  # ticks per wall-clock second, exact and positive


class OffsetTable:
    """Synthetic piecewise-linear wall-clock <-> time-point conversion table.

    Each anchor pins a wall-clock reading to a time point; from that anchor
    up to the next one, time advances at ``rate`` ticks per second.
    """

    def __init__(self, anchors: Iterable[Tuple[WallClock, TimePoint, object]]):
        entries = [
            _Anchor(wall, point, Fraction(rate)) for wall, point, rate in anchors
        ]
        if not entries:
            raise ValueError("OffsetTable needs at least one anchor")
        entries.sort(key=lambda a: a.wall.at)
        for prev, nxt in zip(entries, entries[1:]):
            if not prev.wall.at < nxt.wall.at:
                raise ValueError("anchors must have strictly increasing wall clocks")
            if not prev.point < nxt.point:
                raise ValueError("anchors must have strictly increasing time points")
        for entry in entries:
            if entry.rate <= 0:
                raise ValueError("rate must be positive")
        self._anchors = tuple(entries)

    @property
    def anchors(self) -> Tuple[_Anchor, ...]:
        return self._anchors

    def _anchor_for_wall(self, wall: WallClock) -> _Anchor:
        chosen = self._anchors[0]
        for anchor in self._anchors:
            if anchor.wall.at <= wall.at:
                chosen = anchor
            else:
                break
        return chosen

    def _anchor_for_point(self, point: TimePoint) -> _Anchor:
        chosen = self._anchors[0]
        for anchor in self._anchors:
            if anchor.point <= point:
                chosen = anchor
            else:
                break
        return chosen

    def to_time_point(self, wall: WallClock) -> TimePoint:
        """Exact wall-clock -> time-point conversion."""
        anchor = self._anchor_for_wall(wall)
        delta = wall.at - anchor.wall.at
        micros = Fraction(
            delta.days * 86_400_000_000
            + delta.seconds * 1_000_000
            + delta.microseconds
        )
        return TimePoint(anchor.point.ticks + micros * anchor.rate / _MICROS_PER_SECOND)

    def to_wall_clock(self, point: TimePoint) -> WallClock:
        """Exact time-point -> wall-clock conversion.

        Raises ValueError if the result is not an exact number of
        microseconds (the finest wall-clock resolution).
        """
        anchor = self._anchor_for_point(point)
        micros = (point.ticks - anchor.point.ticks) * _MICROS_PER_SECOND / anchor.rate
        if micros.denominator != 1:
            raise ValueError("time point is not an exact wall-clock microsecond")
        return WallClock(anchor.wall.at + timedelta(microseconds=int(micros)))
