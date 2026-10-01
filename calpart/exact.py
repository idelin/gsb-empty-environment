"""Exact time primitives.

Three distinct concepts are modelled and must never be confused:

* :class:`WallTime` -- a naive local wall-clock reading (a calendar date plus
  a time of day).  It has *no* position on the physical timeline.
* :class:`Instant` -- a point on the UTC timeline (exact seconds since the
  1970-01-01T00:00:00Z epoch).
* :class:`Duration` -- an exact length of time with no position anywhere.

All arithmetic uses :class:`fractions.Fraction`, so endpoints and durations
are exact: no floating-point rounding ever creeps in.
"""

from __future__ import annotations

from datetime import date, timedelta
from fractions import Fraction
from functools import total_ordering

SECONDS_PER_DAY = 86400
EPOCH_DATE = date(1970, 1, 1)


def as_fraction(value) -> Fraction:
    """Coerce ints, strings, Fractions, Instant/Duration to a Fraction of seconds."""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, (Instant, Duration)):
        return value.seconds
    return Fraction(value)


@total_ordering
class Duration:
    """An exact length of time (no position on any timeline)."""

    __slots__ = ("seconds",)

    def __init__(self, seconds=0):
        self.seconds = as_fraction(seconds)

    @classmethod
    def hours(cls, n) -> "Duration":
        return cls(as_fraction(n) * 3600)

    @classmethod
    def days(cls, n) -> "Duration":
        return cls(as_fraction(n) * SECONDS_PER_DAY)

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self.seconds + other.seconds)
        return NotImplemented

    def __radd__(self, other):
        if other == 0:
            return self
        if isinstance(other, Duration):
            return Duration(other.seconds + self.seconds)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self.seconds - other.seconds)
        return NotImplemented

    def __mul__(self, factor):
        return Duration(self.seconds * as_fraction(factor))

    __rmul__ = __mul__

    def __truediv__(self, other):
        if isinstance(other, Duration):
            return self.seconds / other.seconds
        return Duration(self.seconds / as_fraction(other))

    def __eq__(self, other):
        if isinstance(other, Duration):
            return self.seconds == other.seconds
        return NotImplemented

    def __lt__(self, other):
        if isinstance(other, Duration):
            return self.seconds < other.seconds
        return NotImplemented

    def __hash__(self):
        return hash((Duration, self.seconds))

    def __repr__(self):
        return f"Duration({self.seconds})"


@total_ordering
class Instant:
    """A point on the UTC timeline: exact seconds since 1970-01-01T00:00:00Z."""

    __slots__ = ("seconds",)

    def __init__(self, seconds=0):
        self.seconds = as_fraction(seconds)

    def __add__(self, other):
        if isinstance(other, Duration):
            return Instant(self.seconds + other.seconds)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Instant(self.seconds - other.seconds)
        if isinstance(other, Instant):
            return Duration(self.seconds - other.seconds)
        return NotImplemented

    def __eq__(self, other):
        if isinstance(other, Instant):
            return self.seconds == other.seconds
        return NotImplemented

    def __lt__(self, other):
        if isinstance(other, Instant):
            return self.seconds < other.seconds
        return NotImplemented

    def __hash__(self):
        return hash((Instant, self.seconds))

    def __repr__(self):
        return f"Instant({self.seconds})"


@total_ordering
class WallTime:
    """A naive local wall-clock reading: a calendar date plus a time of day.

    Deliberately *not* a point on the physical timeline; converting to or
    from :class:`Instant` requires an offset table (see ``calpart.offsets``).
    """

    __slots__ = ("day", "seconds")

    def __init__(self, day: date, seconds=0):
        s = as_fraction(seconds)
        if not 0 <= s < SECONDS_PER_DAY:
            raise ValueError(f"time-of-day out of range: {s}")
        self.day = day
        self.seconds = s

    @classmethod
    def from_naive_seconds(cls, total) -> "WallTime":
        """Build from naive seconds since 1970-01-01 (no zone semantics)."""
        total = as_fraction(total)
        days = total // SECONDS_PER_DAY
        rem = total - days * SECONDS_PER_DAY
        return cls(EPOCH_DATE + timedelta(days=int(days)), rem)

    def to_naive_seconds(self) -> Fraction:
        return Fraction((self.day - EPOCH_DATE).days * SECONDS_PER_DAY) + self.seconds

    def __eq__(self, other):
        if isinstance(other, WallTime):
            return self.day == other.day and self.seconds == other.seconds
        return NotImplemented

    def __lt__(self, other):
        if isinstance(other, WallTime):
            return self.to_naive_seconds() < other.to_naive_seconds()
        return NotImplemented

    def __hash__(self):
        return hash((WallTime, self.day, self.seconds))

    def __repr__(self):
        whole = int(self.seconds)
        frac = self.seconds - whole
        hh, rem = divmod(whole, 3600)
        mm, ss = divmod(rem, 60)
        text = f"{self.day.isoformat()} {hh:02d}:{mm:02d}:{ss:02d}"
        if frac:
            text += f"+{frac}s"
        return f"WallTime({text})"


class Interval:
    """Half-open UTC interval ``[start, end)`` with exact Fraction endpoints (seconds)."""

    __slots__ = ("start", "end")

    def __init__(self, start, end):
        self.start = as_fraction(start)
        self.end = as_fraction(end)
        if self.start > self.end:
            raise ValueError(f"empty or inverted interval: [{self.start}, {self.end})")

    @classmethod
    def between(cls, a: Instant, b: Instant) -> "Interval":
        return cls(a.seconds, b.seconds)

    @property
    def length(self) -> Fraction:
        return self.end - self.start

    @property
    def duration(self) -> Duration:
        return Duration(self.length)

    @property
    def is_empty(self) -> bool:
        return self.start == self.end

    def contains(self, t) -> bool:
        return self.start <= as_fraction(t) < self.end

    def intersection(self, other: "Interval"):
        lo = max(self.start, other.start)
        hi = min(self.end, other.end)
        if lo >= hi:
            return None
        return Interval(lo, hi)

    def __eq__(self, other):
        if isinstance(other, Interval):
            return self.start == other.start and self.end == other.end
        return NotImplemented

    def __hash__(self):
        return hash((Interval, self.start, self.end))

    def __repr__(self):
        return f"Interval({self.start}, {self.end})"
