"""Exact time primitives: wall-clock times, time points and durations.

Three distinct concepts are modelled as three distinct types:

* :class:`WallClockTime` -- an instant on the wall-clock timeline.
* :class:`TimePoint`     -- an instant on a named (local) clock timeline.
* :class:`Duration`      -- a signed length of time.

All quantities are exact :class:`fractions.Fraction` values; floats are
rejected at the boundary so no binary rounding can leak into projections.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from functools import total_ordering

__all__ = ["as_fraction", "Duration", "TimePoint", "WallClockTime"]


def as_fraction(value) -> Fraction:
    """Convert ``int`` / ``str`` / ``Fraction`` to an exact Fraction.

    Floats are rejected on purpose: they cannot represent time exactly.
    """
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("booleans are not valid time values")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(f"cannot represent {value!r} exactly; use int, str or Fraction")


@total_ordering
@dataclass(frozen=True)
class Duration:
    """A signed, exact length of time (difference of two time points)."""

    seconds: Fraction = Fraction(0)

    def __init__(self, seconds=0):
        object.__setattr__(self, "seconds", as_fraction(seconds))

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self.seconds + other.seconds)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self.seconds - other.seconds)
        return NotImplemented

    def __neg__(self):
        return Duration(-self.seconds)

    def __mul__(self, factor):
        return Duration(self.seconds * as_fraction(factor))

    __rmul__ = __mul__

    def __truediv__(self, other):
        if isinstance(other, Duration):
            return self.seconds / other.seconds
        return Duration(self.seconds / as_fraction(other))

    def __lt__(self, other):
        if not isinstance(other, Duration):
            return NotImplemented
        return self.seconds < other.seconds

    def __repr__(self):
        return f"Duration({self.seconds})"


@total_ordering
@dataclass(frozen=True)
class TimePoint:
    """An instant on a named clock timeline (seconds since that clock's epoch)."""

    clock: str
    offset: Fraction = Fraction(0)

    def __init__(self, clock, offset=0):
        object.__setattr__(self, "clock", clock)
        object.__setattr__(self, "offset", as_fraction(offset))

    def _require_same_clock(self, other: "TimePoint") -> None:
        if self.clock != other.clock:
            raise ValueError(f"clock mismatch: {self.clock!r} vs {other.clock!r}")

    def __add__(self, other):
        if isinstance(other, Duration):
            return TimePoint(self.clock, self.offset + other.seconds)
        if isinstance(other, TimePoint):
            raise TypeError("cannot add two time points")
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return TimePoint(self.clock, self.offset - other.seconds)
        if isinstance(other, TimePoint):
            self._require_same_clock(other)
            return Duration(self.offset - other.offset)
        return NotImplemented

    def __lt__(self, other):
        if not isinstance(other, TimePoint):
            return NotImplemented
        self._require_same_clock(other)
        return self.offset < other.offset


class WallClockTime(TimePoint):
    """An instant on the wall-clock timeline (the distinguished ``wall`` clock)."""

    CLOCK_ID = "wall"

    def __init__(self, offset=0):
        super().__init__(self.CLOCK_ID, offset)
