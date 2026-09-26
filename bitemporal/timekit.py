"""Exact time primitives for the bitemporal store.

Three distinct concepts are modelled as three distinct, non-interchangeable
types:

* ``Duration``      -- a length of time (no position on any timeline).
* ``TimePoint``     -- an exact point on the *synthetic* (logical) timeline.
* ``WallClockTime`` -- an exact point on the wall-clock (UTC) timeline.

All arithmetic is exact: every value is stored as a ``Fraction`` of
microseconds, so no rounding ever occurs.  Mixing timelines in arithmetic
(e.g. ``TimePoint + WallClockTime``) raises ``TypeError``.

``SyntheticOffsetTable`` converts between the synthetic timeline and the
wall-clock timeline using a table of anchor points plus a fixed rate.
"""

from __future__ import annotations

from bisect import bisect_right
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from fractions import Fraction

_MICROS_PER_SECOND = 1_000_000
_EPOCH = datetime(1970, 1, 1, tzinfo=timezone.utc)


@dataclass(frozen=True, order=True)
class Duration:
    """An exact length of time, stored as fractional microseconds."""

    micros: Fraction

    def __init__(self, micros=0):
        object.__setattr__(self, "micros", Fraction(micros))

    @classmethod
    def microseconds(cls, n) -> "Duration":
        return cls(Fraction(n))

    @classmethod
    def milliseconds(cls, n) -> "Duration":
        return cls(Fraction(n) * 1_000)

    @classmethod
    def seconds(cls, n) -> "Duration":
        return cls(Fraction(n) * _MICROS_PER_SECOND)

    @classmethod
    def minutes(cls, n) -> "Duration":
        return cls(Fraction(n) * 60 * _MICROS_PER_SECOND)

    @classmethod
    def hours(cls, n) -> "Duration":
        return cls(Fraction(n) * 3600 * _MICROS_PER_SECOND)

    def total_seconds(self) -> Fraction:
        return self.micros / _MICROS_PER_SECOND

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self.micros + other.micros)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self.micros - other.micros)
        return NotImplemented

    def __mul__(self, factor):
        if isinstance(factor, (int, Fraction)):
            return Duration(self.micros * factor)
        return NotImplemented

    __rmul__ = __mul__

    def __truediv__(self, divisor):
        if isinstance(divisor, (int, Fraction)):
            return Duration(self.micros / divisor)
        if isinstance(divisor, Duration):
            return self.micros / divisor.micros
        return NotImplemented

    def __neg__(self):
        return Duration(-self.micros)


@dataclass(frozen=True, order=True)
class TimePoint:
    """An exact point on the synthetic timeline (microseconds from epoch 0)."""

    micros: Fraction

    def __init__(self, micros=0):
        object.__setattr__(self, "micros", Fraction(micros))

    @classmethod
    def at(cls, *, seconds=0, micros=0) -> "TimePoint":
        return cls(Fraction(seconds) * _MICROS_PER_SECOND + Fraction(micros))

    def __add__(self, other):
        if isinstance(other, Duration):
            return TimePoint(self.micros + other.micros)
        return NotImplemented

    __radd__ = __add__

    def __sub__(self, other):
        if isinstance(other, Duration):
            return TimePoint(self.micros - other.micros)
        if isinstance(other, TimePoint):
            return Duration(self.micros - other.micros)
        return NotImplemented


@dataclass(frozen=True, order=True)
class WallClockTime:
    """An exact point on the wall-clock timeline (UTC microseconds since epoch)."""

    micros: Fraction

    def __init__(self, micros=0):
        object.__setattr__(self, "micros", Fraction(micros))

    @classmethod
    def from_datetime(cls, dt: datetime) -> "WallClockTime":
        if dt.tzinfo is None:
            raise ValueError("WallClockTime requires a timezone-aware datetime")
        delta = dt.astimezone(timezone.utc) - _EPOCH
        micros = (
            delta.days * 86_400 * _MICROS_PER_SECOND
            + delta.seconds * _MICROS_PER_SECOND
            + delta.microseconds
        )
        return cls(Fraction(micros))

    def to_datetime(self) -> datetime:
        if self.micros.denominator != 1:
            raise ValueError(
                "not representable as datetime: sub-microsecond fraction "
                f"{self.micros}"
            )
        return _EPOCH + timedelta(microseconds=int(self.micros))

    def __add__(self, other):
        if isinstance(other, Duration):
            return WallClockTime(self.micros + other.micros)
        return NotImplemented

    __radd__ = __add__

    def __sub__(self, other):
        if isinstance(other, Duration):
            return WallClockTime(self.micros - other.micros)
        if isinstance(other, WallClockTime):
            return Duration(self.micros - other.micros)
        return NotImplemented


class SyntheticOffsetTable:
    """Piecewise conversion table between synthetic and wall-clock timelines.

    Anchors pin synthetic ``TimePoint``s to ``WallClockTime``s.  Conversion
    between anchors uses a fixed *rate* (wall-clock micros per synthetic
    micro, default 1:1).  All conversions are exact ``Fraction`` arithmetic.
    """

    def __init__(self, rate=Fraction(1)):
        self._rate = Fraction(rate)
        if self._rate <= 0:
            raise ValueError("rate must be positive")
        self._anchors: list[tuple[TimePoint, WallClockTime]] = []

    def add_anchor(self, point: TimePoint, wall: WallClockTime) -> None:
        if not isinstance(point, TimePoint) or not isinstance(wall, WallClockTime):
            raise TypeError("anchors must be (TimePoint, WallClockTime)")
        points = [a[0] for a in self._anchors]
        i = bisect_right(points, point)
        if i > 0 and points[i - 1] == point:
            raise ValueError(f"duplicate anchor at {point!r}")
        if i > 0 and not (self._anchors[i - 1][1] < wall):
            raise ValueError("anchors must be strictly increasing on both timelines")
        if i < len(self._anchors) and not (wall < self._anchors[i][1]):
            raise ValueError("anchors must be strictly increasing on both timelines")
        self._anchors.insert(i, (point, wall))

    def _anchor_for_point(self, point: TimePoint) -> tuple[TimePoint, WallClockTime]:
        if not self._anchors:
            raise ValueError("conversion table has no anchors")
        points = [a[0] for a in self._anchors]
        i = bisect_right(points, point) - 1
        return self._anchors[max(0, i)]

    def _anchor_for_wall(self, wall: WallClockTime) -> tuple[TimePoint, WallClockTime]:
        if not self._anchors:
            raise ValueError("conversion table has no anchors")
        walls = [a[1] for a in self._anchors]
        i = bisect_right(walls, wall) - 1
        return self._anchors[max(0, i)]

    def to_wall_clock(self, point: TimePoint) -> WallClockTime:
        if not isinstance(point, TimePoint):
            raise TypeError("expected TimePoint")
        a_point, a_wall = self._anchor_for_point(point)
        return a_wall + (point - a_point) * self._rate

    def to_time_point(self, wall: WallClockTime) -> TimePoint:
        if not isinstance(wall, WallClockTime):
            raise TypeError("expected WallClockTime")
        a_point, a_wall = self._anchor_for_wall(wall)
        return a_point + (wall - a_wall) / self._rate
