"""Exact time values: wall-clock readings, absolute time points, durations.

Three distinct concepts are modelled as three distinct types so they can
never be silently mixed:

- WallClock: a raw reading taken from a source-local clock. It has no
  arithmetic; it must be converted through an OffsetTable first.
- TimePoint: an absolute point on the reference timeline.
- Duration: a length of time.

All values are fractions.Fraction, so arithmetic is exact.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


def _frac(value) -> Fraction:
    return value if isinstance(value, Fraction) else Fraction(value)


@dataclass(frozen=True)
class Duration:
    value: Fraction

    def __init__(self, value=0):
        object.__setattr__(self, "value", _frac(value))

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self.value + other.value)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self.value - other.value)
        return NotImplemented

    def __neg__(self):
        return Duration(-self.value)

    def __abs__(self):
        return Duration(abs(self.value))


@dataclass(frozen=True)
class TimePoint:
    value: Fraction

    def __init__(self, value=0):
        object.__setattr__(self, "value", _frac(value))

    def __add__(self, other):
        if isinstance(other, Duration):
            return TimePoint(self.value + other.value)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return TimePoint(self.value - other.value)
        if isinstance(other, TimePoint):
            return Duration(self.value - other.value)
        return NotImplemented

    def __lt__(self, other: "TimePoint") -> bool:
        return self.value < other.value

    def __le__(self, other: "TimePoint") -> bool:
        return self.value <= other.value


@dataclass(frozen=True)
class WallClock:
    source: str
    reading: Fraction

    def __init__(self, source: str, reading):
        object.__setattr__(self, "source", source)
        object.__setattr__(self, "reading", _frac(reading))


@dataclass(frozen=True)
class Interval:
    """Closed uncertainty interval [lo, hi] on the reference timeline."""

    lo: TimePoint
    hi: TimePoint

    def overlaps(self, other: "Interval") -> bool:
        return self.lo.value <= other.hi.value and other.lo.value <= self.hi.value

    def definitely_before(self, other: "Interval") -> bool:
        return self.hi.value < other.lo.value
