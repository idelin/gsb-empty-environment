"""Exact time primitives.

Two different concepts are modelled and kept distinct at the type level:

* :class:`WallClock` -- a *point* in wall-clock time (seconds since a
  reference).  You may subtract two points to get a :class:`Duration`, or
  shift a point by a duration, but adding two points is meaningless and is
  rejected by the type system (``TypeError``).
* :class:`Duration` -- a *length* of time (seconds).  Durations can be
  added, subtracted, scaled and compared.

Both are backed by :class:`fractions.Fraction`, so every computation in this
package is exact rational arithmetic.  Floats are refused at construction
time so inexact values can never leak in (e.g. two timestamps that differ by
one microsecond around 1e15 seconds are still distinguishable, while
``float`` would merge them).
"""

from __future__ import annotations

from fractions import Fraction
from functools import total_ordering
from typing import Union

NumberLike = Union[int, Fraction]


def _as_fraction(value: NumberLike, owner: str) -> Fraction:
    if isinstance(value, bool) or not isinstance(value, (int, Fraction)):
        raise TypeError(
            f"{owner} requires an exact int or Fraction, got {type(value).__name__}; "
            "floats are rejected to keep time representation exact"
        )
    return Fraction(value)


@total_ordering
class Duration:
    """An exact length of time, in seconds."""

    __slots__ = ("_seconds",)

    def __init__(self, seconds: NumberLike = 0):
        self._seconds = _as_fraction(seconds, "Duration")

    @property
    def seconds(self) -> Fraction:
        """The length as an exact :class:`~fractions.Fraction` of seconds."""
        return self._seconds

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self._seconds + other._seconds)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self._seconds - other._seconds)
        return NotImplemented

    def __mul__(self, factor):
        if isinstance(factor, (int, Fraction)) and not isinstance(factor, bool):
            return Duration(self._seconds * factor)
        return NotImplemented

    __rmul__ = __mul__

    def __truediv__(self, other):
        if isinstance(other, Duration):
            return self._seconds / other._seconds
        if isinstance(other, (int, Fraction)) and not isinstance(other, bool):
            return Duration(self._seconds / other)
        return NotImplemented

    def __neg__(self):
        return Duration(-self._seconds)

    def __abs__(self):
        return Duration(abs(self._seconds))

    def __eq__(self, other):
        if isinstance(other, Duration):
            return self._seconds == other._seconds
        return NotImplemented

    def __lt__(self, other):
        if isinstance(other, Duration):
            return self._seconds < other._seconds
        return NotImplemented

    def __hash__(self):
        return hash(("Duration", self._seconds))

    def __repr__(self):
        return f"Duration({self._seconds})"


@total_ordering
class WallClock:
    """An exact point in wall-clock time, in seconds since a reference."""

    __slots__ = ("_seconds",)

    def __init__(self, seconds: NumberLike = 0):
        self._seconds = _as_fraction(seconds, "WallClock")

    @property
    def seconds(self) -> Fraction:
        """The point as an exact :class:`~fractions.Fraction` of seconds."""
        return self._seconds

    def __add__(self, other):
        if isinstance(other, Duration):
            return WallClock(self._seconds + other._seconds)
        # Notably WallClock + WallClock is not defined -> TypeError.
        return NotImplemented

    __radd__ = __add__

    def __sub__(self, other):
        if isinstance(other, WallClock):
            return Duration(self._seconds - other._seconds)
        if isinstance(other, Duration):
            return WallClock(self._seconds - other._seconds)
        return NotImplemented

    def __eq__(self, other):
        if isinstance(other, WallClock):
            return self._seconds == other._seconds
        return NotImplemented

    def __lt__(self, other):
        if isinstance(other, WallClock):
            return self._seconds < other._seconds
        return NotImplemented

    def __hash__(self):
        return hash(("WallClock", self._seconds))

    def __repr__(self):
        return f"WallClock({self._seconds})"
