"""Exact time primitives.

Three distinct concepts are modelled by three distinct types:

* ``WallClock`` -- a calendar/clock reading (what a wall clock shows), UTC.
* ``Instant``   -- a point on the abstract monotonic timeline (seconds since
  an epoch), used for both local and reference clock readings.
* ``Duration``  -- a length of time; the difference of two ``Instant``\\ s.

All arithmetic is exact (``fractions.Fraction``); floats are rejected so no
rounding can sneak into the time representation.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from fractions import Fraction

_EPOCH = datetime(1970, 1, 1, tzinfo=timezone.utc)
_US_PER_S = 1_000_000


class TimeTypeError(TypeError):
    """Raised when incompatible time concepts are mixed."""


def _fraction(value, what):
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int) and not isinstance(value, bool):
        return Fraction(value)
    raise TimeTypeError(f"{what} must be an int or Fraction, got {type(value).__name__}")


@dataclass(frozen=True, order=True)
class Duration:
    """A length of time: the difference of two Instants. Exact seconds."""

    seconds: Fraction = Fraction(0)

    def __init__(self, seconds=0, *, milliseconds=0, microseconds=0):
        total = _fraction(seconds, "seconds")
        total += _fraction(milliseconds, "milliseconds") / 1_000
        total += _fraction(microseconds, "microseconds") / _US_PER_S
        object.__setattr__(self, "seconds", total)

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self.seconds + other.seconds)
        if isinstance(other, (Instant, WallClock)):
            return other + self
        return NotImplemented

    __radd__ = __add__

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self.seconds - other.seconds)
        return NotImplemented

    def __neg__(self):
        return Duration(-self.seconds)

    def __mul__(self, factor):
        return Duration(self.seconds * _fraction(factor, "factor"))

    __rmul__ = __mul__

    def __truediv__(self, other):
        if isinstance(other, Duration):
            return self.seconds / other.seconds
        return Duration(self.seconds / _fraction(other, "divisor"))

    def __repr__(self):
        return f"Duration({self.seconds})"


@dataclass(frozen=True, order=True)
class Instant:
    """A point on the abstract monotonic timeline: exact seconds since epoch."""

    seconds: Fraction = Fraction(0)

    def __init__(self, seconds=0):
        object.__setattr__(self, "seconds", _fraction(seconds, "seconds"))

    def __add__(self, other):
        if isinstance(other, Duration):
            return Instant(self.seconds + other.seconds)
        if isinstance(other, (Instant, WallClock)):
            raise TimeTypeError("cannot add two time points")
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Instant):
            return Duration(self.seconds - other.seconds)
        if isinstance(other, Duration):
            return Instant(self.seconds - other.seconds)
        if isinstance(other, WallClock):
            raise TimeTypeError("cannot subtract a wall-clock reading from an instant")
        return NotImplemented

    def to_wallclock(self):
        return WallClock.from_instant(self)

    def __repr__(self):
        return f"Instant({self.seconds})"


@dataclass(frozen=True, order=True)
class WallClock:
    """A calendar/clock reading (always normalised to UTC).

    Microsecond resolution; conversion to/from ``Instant`` is exact and
    refuses values that are not microsecond-representable.
    """

    moment: datetime

    def __init__(self, moment):
        if not isinstance(moment, datetime):
            raise TimeTypeError("WallClock needs a datetime")
        if moment.tzinfo is None:
            raise TimeTypeError("WallClock needs a timezone-aware datetime")
        object.__setattr__(self, "moment", moment.astimezone(timezone.utc))

    @classmethod
    def from_iso(cls, text):
        return cls(datetime.fromisoformat(text))

    @classmethod
    def from_instant(cls, instant):
        us = instant.seconds * _US_PER_S
        if us.denominator != 1:
            raise ValueError(f"instant {instant.seconds}s is not microsecond-exact")
        return cls(_EPOCH + timedelta(microseconds=int(us)))

    def to_instant(self):
        delta = self.moment - _EPOCH
        us = (delta.days * 86_400 + delta.seconds) * _US_PER_S + delta.microseconds
        return Instant(Fraction(us, _US_PER_S))

    def __add__(self, other):
        if isinstance(other, Duration):
            return WallClock.from_instant(self.to_instant() + other)
        if isinstance(other, (Instant, WallClock)):
            raise TimeTypeError("cannot add two time points")
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, WallClock):
            return self.to_instant() - other.to_instant()
        if isinstance(other, Duration):
            return self + (-other)
        if isinstance(other, Instant):
            raise TimeTypeError("cannot subtract an instant from a wall-clock reading")
        return NotImplemented

    def isoformat(self):
        return self.moment.isoformat()

    def __repr__(self):
        return f"WallClock({self.moment.isoformat()})"
