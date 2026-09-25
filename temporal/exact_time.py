"""Exact time model.

Three distinct concepts are modeled as three distinct types and are NOT
interchangeable:

- ``Duration``  : a length of time (exact ``Fraction`` of a second).
- ``TimePoint`` : an instant on the absolute timeline (epoch + ``Duration``).
- ``WallClock`` : a civil clock reading in a named offset zone; it is not an
  instant until converted through an ``OffsetTable``.

All arithmetic is exact rational arithmetic (``fractions.Fraction``).  No
floats and no epsilon tolerances are used anywhere, so a conflict of
e.g. 1/3 of a second is detected exactly.
"""

from __future__ import annotations

from datetime import date
from fractions import Fraction
from functools import total_ordering


@total_ordering
class Duration:
    """An exact length of time, stored as a Fraction of a second."""

    __slots__ = ("_seconds",)

    def __init__(self, seconds=0):
        self._seconds = Fraction(seconds)

    @classmethod
    def seconds(cls, n):
        return cls(Fraction(n))

    @classmethod
    def minutes(cls, n):
        return cls(Fraction(n) * 60)

    @classmethod
    def hours(cls, n):
        return cls(Fraction(n) * 3600)

    @classmethod
    def days(cls, n):
        return cls(Fraction(n) * 86400)

    @property
    def as_fraction(self) -> Fraction:
        """Exact number of seconds as a Fraction."""
        return self._seconds

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self._seconds + other._seconds)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self._seconds - other._seconds)
        return NotImplemented

    def __neg__(self):
        return Duration(-self._seconds)

    def __mul__(self, k):
        if isinstance(k, (int, Fraction)):
            return Duration(self._seconds * k)
        return NotImplemented

    __rmul__ = __mul__

    def __eq__(self, other):
        if isinstance(other, Duration):
            return self._seconds == other._seconds
        return NotImplemented

    def __lt__(self, other):
        if isinstance(other, Duration):
            return self._seconds < other._seconds
        return NotImplemented

    def __hash__(self):
        return hash(self._seconds)

    def __repr__(self):
        return f"Duration({self._seconds}s)"


@total_ordering
class TimePoint:
    """An absolute instant: epoch + exact offset Duration."""

    __slots__ = ("_offset",)

    def __init__(self, offset: Duration):
        if not isinstance(offset, Duration):
            raise TypeError("TimePoint offset must be a Duration")
        self._offset = offset

    @property
    def offset(self) -> Duration:
        """Exact Duration since the epoch."""
        return self._offset

    def __add__(self, other):
        if isinstance(other, Duration):
            return TimePoint(self._offset + other)
        return NotImplemented  # TimePoint + TimePoint is a TypeError

    def __sub__(self, other):
        if isinstance(other, TimePoint):
            return Duration(self._offset.as_fraction - other._offset.as_fraction)
        if isinstance(other, Duration):
            return TimePoint(self._offset - other)
        return NotImplemented

    def __eq__(self, other):
        if isinstance(other, TimePoint):
            return self._offset == other._offset
        return NotImplemented

    def __lt__(self, other):
        if isinstance(other, TimePoint):
            return self._offset < other._offset
        return NotImplemented

    def __hash__(self):
        return hash(self._offset)

    def __repr__(self):
        return f"TimePoint({self._offset.as_fraction}s)"


class WallClock:
    """A civil wall-clock reading in a named offset zone.

    Deliberately NOT a TimePoint: arithmetic between WallClocks of different
    zones is meaningless until converted through an OffsetTable.
    """

    __slots__ = ("day", "time_of_day", "zone")

    def __init__(self, day: date, time_of_day: Duration, zone: str):
        if not isinstance(time_of_day, Duration):
            raise TypeError("time_of_day must be a Duration")
        self.day = day
        self.time_of_day = time_of_day
        self.zone = zone

    def __repr__(self):
        return f"WallClock({self.day.isoformat()} {self.time_of_day.as_fraction}s {self.zone})"


class OffsetTable:
    """Synthetic offset conversion table.

    Maps zone names to exact UTC offsets.  Entries may be *derived* from
    other entries plus a delta, composing synthetic offsets (e.g. a shift
    schedule on top of a legal time zone).
    """

    EPOCH = date(1970, 1, 1)

    def __init__(self):
        self._table = {}

    def define(self, name: str, offset: Duration) -> None:
        self._table[name] = offset

    def derive(self, name: str, base: str, delta: Duration) -> None:
        """Define ``name`` as ``base``'s offset plus ``delta`` (synthetic)."""
        self._table[name] = self.offset(base) + delta

    def offset(self, name: str) -> Duration:
        if name not in self._table:
            raise KeyError(f"unknown offset zone: {name!r}")
        return self._table[name]

    def to_timepoint(self, clock: WallClock) -> TimePoint:
        """Convert a WallClock reading to an exact absolute TimePoint."""
        days = (clock.day - self.EPOCH).days
        utc = Duration.days(days) + clock.time_of_day - self.offset(clock.zone)
        return TimePoint(utc)
