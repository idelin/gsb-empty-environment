"""Exact time primitives.

Three notions are kept type-safe apart:
- ``Instant``: an absolute UTC time point.
- ``Wall``: a local wall-clock reading (no offset attached).
- ``Duration``: an exact elapsed length.

Every value carries a ``Fraction`` of seconds since the proleptic Gregorian
epoch 1970-01-01T00:00:00, so endpoints are exact numbers, never floats.
Civil-date conversion uses pure integer arithmetic (Howard Hinnant's
algorithms), so any year is supported, not just datetime's 1..9999 range.
"""
from __future__ import annotations

from fractions import Fraction
from functools import total_ordering

SECONDS_PER_DAY = 86_400
EPOCH_WEEKDAY = 3  # 1970-01-01 was a Thursday (Monday == 0)
_DAYS_PER_ERA = 146_097  # one 400-year Gregorian cycle
_GREGORIAN_EPOCH_OFFSET = 719_468  # days from 0000-03-01 to 1970-01-01


def as_fraction(value) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("bool is not an exact time value")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, float):
        raise TypeError("float is not exact; use int or Fraction")
    return Fraction(value)


def days_from_civil(year: int, month: int, day: int) -> int:
    """Days from 1970-01-01 to the given proleptic Gregorian date."""
    y = year - (1 if month <= 2 else 0)
    era = y // 400
    yoe = y - era * 400
    mp = month - 3 if month > 2 else month + 9
    doy = (153 * mp + 2) // 5 + day - 1
    doe = yoe * 365 + yoe // 4 - yoe // 100 + doy
    return era * _DAYS_PER_ERA + doe - _GREGORIAN_EPOCH_OFFSET


def civil_from_days(days: int) -> tuple[int, int, int]:
    """The (year, month, day) of the date ``days`` days after 1970-01-01."""
    z = days + _GREGORIAN_EPOCH_OFFSET
    era = z // _DAYS_PER_ERA
    doe = z - era * _DAYS_PER_ERA
    yoe = (doe - doe // 1460 + doe // 36524 - doe // 146096) // 365
    year = yoe + era * 400
    doy = doe - (365 * yoe + yoe // 4 - yoe // 100)
    mp = (5 * doy + 2) // 153
    day = doy - (153 * mp + 2) // 5 + 1
    month = mp + 3 if mp < 10 else mp - 9
    return year + (1 if month <= 2 else 0), month, day


def split_days(seconds: Fraction) -> tuple[int, Fraction]:
    days = seconds // SECONDS_PER_DAY
    return days, seconds - days * SECONDS_PER_DAY


def format_civil(seconds: Fraction) -> str:
    """Render exact seconds as an ISO-like civil string (no zone suffix)."""
    seconds = as_fraction(seconds)
    days, rem = split_days(seconds)
    year, month, day = civil_from_days(days)
    hour = rem // 3600
    rem -= hour * 3600
    minute = rem // 60
    rem -= minute * 60
    text = f"{year:04d}-{month:02d}-{day:02d}T{hour:02d}:{minute:02d}:{int(rem):02d}"
    frac = rem - int(rem)
    if frac:
        text += f" (+{frac} s)"
    return text


def _civil_seconds(year, month, day, hour=0, minute=0, second=0, fraction=0) -> Fraction:
    whole = days_from_civil(year, month, day) * SECONDS_PER_DAY + hour * 3600 + minute * 60 + second
    return Fraction(whole) + as_fraction(fraction)


@total_ordering
class Duration:
    """An exact elapsed length; not anchored to any timeline."""

    __slots__ = ("seconds",)

    def __init__(self, seconds=0):
        self.seconds = as_fraction(seconds)

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self.seconds + other.seconds)
        return NotImplemented

    __radd__ = __add__

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self.seconds - other.seconds)
        return NotImplemented

    def __neg__(self):
        return Duration(-self.seconds)

    def __mul__(self, factor):
        if isinstance(factor, int):
            return Duration(self.seconds * factor)
        return NotImplemented

    __rmul__ = __mul__

    def __eq__(self, other):
        return isinstance(other, Duration) and self.seconds == other.seconds

    def __lt__(self, other):
        if isinstance(other, Duration):
            return self.seconds < other.seconds
        return NotImplemented

    def __hash__(self):
        return hash((Duration, self.seconds))

    def __repr__(self):
        return f"Duration({self.seconds})"

    def __str__(self):
        return f"{self.seconds} s"


@total_ordering
class Instant:
    """An absolute UTC time point (exact)."""

    __slots__ = ("seconds",)

    def __init__(self, seconds=0):
        self.seconds = as_fraction(seconds)

    @classmethod
    def from_civil(cls, year, month, day, hour=0, minute=0, second=0, fraction=0):
        return cls(_civil_seconds(year, month, day, hour, minute, second, fraction))

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
        return isinstance(other, Instant) and self.seconds == other.seconds

    def __lt__(self, other):
        if isinstance(other, Instant):
            return self.seconds < other.seconds
        return NotImplemented

    def __hash__(self):
        return hash((Instant, self.seconds))

    def __repr__(self):
        return f"Instant({format_civil(self.seconds)}Z)"


@total_ordering
class Wall:
    """A local wall-clock reading; no UTC offset is attached."""

    __slots__ = ("seconds",)

    def __init__(self, seconds=0):
        self.seconds = as_fraction(seconds)

    @classmethod
    def from_civil(cls, year, month, day, hour=0, minute=0, second=0, fraction=0):
        return cls(_civil_seconds(year, month, day, hour, minute, second, fraction))

    def __add__(self, other):
        if isinstance(other, Duration):
            return Wall(self.seconds + other.seconds)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Wall(self.seconds - other.seconds)
        if isinstance(other, Wall):
            return Duration(self.seconds - other.seconds)
        return NotImplemented

    def __eq__(self, other):
        return isinstance(other, Wall) and self.seconds == other.seconds

    def __lt__(self, other):
        if isinstance(other, Wall):
            return self.seconds < other.seconds
        return NotImplemented

    def __hash__(self):
        return hash((Wall, self.seconds))

    def __repr__(self):
        return f"Wall({format_civil(self.seconds)})"


def utc_seconds(value) -> Fraction:
    if isinstance(value, Instant):
        return value.seconds
    return as_fraction(value)


def wall_seconds(value) -> Fraction:
    if isinstance(value, Wall):
        return value.seconds
    return as_fraction(value)
