"""Exact rational time primitives.

Three distinct concepts are modelled and kept type-safe:

- ``WallTime``: a *time point* on the wall-clock timeline (seconds since an
  arbitrary reference). You may subtract two time points (yielding a
  ``Duration``) or shift a time point by a ``Duration``, but adding two time
  points is a ``TypeError``.
- ``Duration``: a *length of time* with no position on the timeline.
- ``TimeInterval``: a closed range of wall-clock time points ``[lo, hi]``.

All arithmetic uses ``fractions.Fraction``; floats are rejected so that no
value is ever silently rounded (e.g. nanosecond neighbours near 2**60
seconds must not be swallowed by float64).
"""
from __future__ import annotations

from fractions import Fraction
from functools import total_ordering


def exact(value) -> Fraction:
    """Coerce ``value`` to an exact ``Fraction``; floats are refused."""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("bool is not a valid time value")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, float):
        raise TypeError("float is not exact; use int or Fraction")
    return Fraction(value)


@total_ordering
class Duration:
    """An exact length of time, in seconds. Not anchored to the timeline."""

    __slots__ = ("seconds",)

    def __init__(self, seconds=0):
        self.seconds = exact(seconds)

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self.seconds + other.seconds)
        if isinstance(other, WallTime):
            return other + self
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self.seconds - other.seconds)
        return NotImplemented

    def __mul__(self, factor):
        if isinstance(factor, (int, Fraction)) and not isinstance(factor, bool):
            return Duration(self.seconds * factor)
        return NotImplemented

    __rmul__ = __mul__

    def __truediv__(self, other):
        if isinstance(other, Duration):
            return self.seconds / other.seconds
        if isinstance(other, (int, Fraction)) and not isinstance(other, bool):
            return Duration(self.seconds / other)
        return NotImplemented

    def __neg__(self):
        return Duration(-self.seconds)

    def __eq__(self, other):
        if isinstance(other, Duration):
            return self.seconds == other.seconds
        return NotImplemented

    def __lt__(self, other):
        if isinstance(other, Duration):
            return self.seconds < other.seconds
        return NotImplemented

    def __hash__(self):
        return hash(("Duration", self.seconds))

    def __float__(self):
        return float(self.seconds)

    def __repr__(self):
        return f"Duration({self.seconds})"


@total_ordering
class WallTime:
    """An exact point on the wall-clock timeline (seconds since a reference)."""

    __slots__ = ("seconds",)

    def __init__(self, seconds):
        self.seconds = exact(seconds)

    def __add__(self, other):
        if isinstance(other, Duration):
            return WallTime(self.seconds + other.seconds)
        if isinstance(other, WallTime):
            raise TypeError("cannot add two wall-clock time points")
        return NotImplemented

    __radd__ = __add__

    def __sub__(self, other):
        if isinstance(other, WallTime):
            return Duration(self.seconds - other.seconds)
        if isinstance(other, Duration):
            return WallTime(self.seconds - other.seconds)
        return NotImplemented

    def __eq__(self, other):
        if isinstance(other, WallTime):
            return self.seconds == other.seconds
        return NotImplemented

    def __lt__(self, other):
        if isinstance(other, WallTime):
            return self.seconds < other.seconds
        return NotImplemented

    def __hash__(self):
        return hash(("WallTime", self.seconds))

    def __float__(self):
        return float(self.seconds)

    def __repr__(self):
        return f"WallTime({self.seconds})"


class TimeInterval:
    """A closed interval of wall-clock time points; may be empty (lo > hi)."""

    __slots__ = ("lo", "hi")

    def __init__(self, lo: WallTime, hi: WallTime):
        if not isinstance(lo, WallTime) or not isinstance(hi, WallTime):
            raise TypeError("TimeInterval endpoints must be WallTime")
        self.lo = lo
        self.hi = hi

    @property
    def is_empty(self) -> bool:
        return self.lo.seconds > self.hi.seconds

    @property
    def width(self) -> Duration:
        return self.hi - self.lo

    def intersect(self, other: "TimeInterval") -> "TimeInterval":
        lo = self.lo if self.lo.seconds >= other.lo.seconds else other.lo
        hi = self.hi if self.hi.seconds <= other.hi.seconds else other.hi
        return TimeInterval(lo, hi)

    def contains(self, point: WallTime) -> bool:
        return self.lo.seconds <= point.seconds <= self.hi.seconds

    def __eq__(self, other):
        if isinstance(other, TimeInterval):
            return self.lo == other.lo and self.hi == other.hi
        return NotImplemented

    def __repr__(self):
        return f"TimeInterval({self.lo!r}, {self.hi!r})"
