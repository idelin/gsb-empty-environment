"""Exact time primitives.

Three distinct notions are modelled and kept type-safe:

* :class:`WallClockTime` -- a reading on a specific physical clock.  It is
  *not* an absolute time; it only becomes one through a calibration table.
* :class:`TimePoint` -- an absolute instant on the reference timeline.
* :class:`Duration` -- a signed length of time.

All values are :class:`fractions.Fraction`, so every computation is exact.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


def _frac(value) -> Fraction:
    return value if isinstance(value, Fraction) else Fraction(value)


@dataclass(frozen=True, order=True)
class Duration:
    """A signed length of time (exact)."""

    value: Fraction = Fraction(0)

    def __post_init__(self):
        object.__setattr__(self, "value", _frac(self.value))

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self.value + other.value)
        if isinstance(other, TimePoint):
            return TimePoint(self.value + other.value)
        if isinstance(other, WallClockTime):
            return WallClockTime(self.value + other.value, other.clock)
        return NotImplemented

    __radd__ = __add__

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self.value - other.value)
        return NotImplemented

    def __neg__(self):
        return Duration(-self.value)

    def __abs__(self):
        return Duration(abs(self.value))

    def __mul__(self, factor):
        return Duration(self.value * _frac(factor))

    __rmul__ = __mul__

    def __truediv__(self, divisor):
        if isinstance(divisor, Duration):
            return self.value / divisor.value
        return Duration(self.value / _frac(divisor))


@dataclass(frozen=True, order=True)
class TimePoint:
    """An absolute instant on the reference timeline (exact)."""

    value: Fraction

    def __post_init__(self):
        object.__setattr__(self, "value", _frac(self.value))

    def __add__(self, other):
        if isinstance(other, Duration):
            return TimePoint(self.value + other.value)
        if isinstance(other, TimePoint):
            raise TypeError("cannot add two absolute time points")
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return TimePoint(self.value - other.value)
        if isinstance(other, TimePoint):
            return Duration(self.value - other.value)
        if isinstance(other, WallClockTime):
            raise TypeError(
                "an absolute time point and a wall-clock reading live on "
                "different timelines; calibrate the clock first")
        return NotImplemented


@dataclass(frozen=True)
class WallClockTime:
    """A reading on a named physical clock; not an absolute time."""

    value: Fraction
    clock: str = "wall"

    def __post_init__(self):
        object.__setattr__(self, "value", _frac(self.value))

    def _peer(self, other):
        if not isinstance(other, WallClockTime):
            raise TypeError(f"expected WallClockTime, got {type(other).__name__}")
        if self.clock != other.clock:
            raise TypeError(
                f"readings from different clocks: {self.clock!r} vs {other.clock!r}")
        return other

    def __add__(self, other):
        if isinstance(other, Duration):
            return WallClockTime(self.value + other.value, self.clock)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, WallClockTime):
            other = self._peer(other)
            return Duration(self.value - other.value)
        if isinstance(other, Duration):
            return WallClockTime(self.value - other.value, self.clock)
        if isinstance(other, TimePoint):
            raise TypeError(
                "a wall-clock reading is not an absolute time point; "
                "apply a calibration table first")
        return NotImplemented

    def __lt__(self, other):
        return self.value < self._peer(other).value

    def __le__(self, other):
        return self.value <= self._peer(other).value

    def __gt__(self, other):
        return self.value > self._peer(other).value

    def __ge__(self, other):
        return self.value >= self._peer(other).value


def _nonempty(lo, lo_open, hi, hi_open) -> bool:
    if lo < hi:
        return True
    if lo == hi:
        return not lo_open and not hi_open
    return False


@dataclass(frozen=True)
class Interval:
    """A non-empty interval with individually open/closed endpoints."""

    start: TimePoint
    end: TimePoint
    start_open: bool = False
    end_open: bool = False

    def __post_init__(self):
        if not _nonempty(self.start.value, self.start_open,
                         self.end.value, self.end_open):
            raise ValueError(f"empty interval: {self}")

    @property
    def duration(self) -> Duration:
        return Duration(self.end.value - self.start.value)

    def contains(self, t: TimePoint) -> bool:
        if t.value < self.start.value or t.value > self.end.value:
            return False
        if t.value == self.start.value and self.start_open:
            return False
        if t.value == self.end.value and self.end_open:
            return False
        return True

    def intersection(self, other: "Interval") -> "Interval | None":
        if self.start.value > other.start.value:
            lo, lo_open = self.start, self.start_open
        elif self.start.value < other.start.value:
            lo, lo_open = other.start, other.start_open
        else:
            lo = self.start
            lo_open = self.start_open or other.start_open
        if self.end.value < other.end.value:
            hi, hi_open = self.end, self.end_open
        elif self.end.value > other.end.value:
            hi, hi_open = other.end, other.end_open
        else:
            hi = self.end
            hi_open = self.end_open or other.end_open
        if not _nonempty(lo.value, lo_open, hi.value, hi_open):
            return None
        return Interval(lo, hi, lo_open, hi_open)

    def subtract(self, other: "Interval") -> list:
        """Return the pieces of ``self`` not covered by ``other``."""
        if self.intersection(other) is None:
            return [self]
        parts = []
        # Points of `other.start` survive iff `other` excludes its own start.
        if _nonempty(self.start.value, self.start_open,
                     other.start.value, not other.start_open):
            parts.append(Interval(self.start, other.start,
                                  self.start_open, not other.start_open))
        if _nonempty(other.end.value, not other.end_open,
                     self.end.value, self.end_open):
            parts.append(Interval(other.end, self.end,
                                  not other.end_open, self.end_open))
        return parts
