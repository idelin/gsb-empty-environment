"""Precise time primitives.

All arithmetic uses Python integers (nanoseconds), so values keep full
precision at arbitrarily large or small epochs -- no floats anywhere.

* ``Instant``   -- a point on the UTC timeline (ns since 1970-01-01T00:00Z).
* ``WallClock`` -- a local date + time-of-day with no offset attached.
* ``Duration``  -- a fixed amount of elapsed time.

Calendar stepping (``plus_days``/``plus_months``/``plus_years``) follows the
proleptic Gregorian calendar and is intentionally distinct from adding a
fixed :class:`Duration`: "one month" is not "30 * 24 hours".
"""

from __future__ import annotations

from dataclasses import dataclass

NANOS_PER_SECOND = 1_000_000_000
NANOS_PER_MINUTE = 60 * NANOS_PER_SECOND
NANOS_PER_HOUR = 60 * NANOS_PER_MINUTE
NANOS_PER_DAY = 24 * NANOS_PER_HOUR

_DAYS_IN_MONTH = (31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31)


def is_leap_year(year: int) -> bool:
    return year % 4 == 0 and (year % 100 != 0 or year % 400 == 0)


def days_in_month(year: int, month: int) -> int:
    if month == 2 and is_leap_year(year):
        return 29
    return _DAYS_IN_MONTH[month - 1]


def days_from_civil(year: int, month: int, day: int) -> int:
    """Days since 1970-01-01 for a proleptic Gregorian date (Hinnant)."""
    y = year - (1 if month <= 2 else 0)
    era = y // 400
    yoe = y - era * 400
    mp = (month + 9) % 12
    doy = (153 * mp + 2) // 5 + day - 1
    doe = yoe * 365 + yoe // 4 - yoe // 100 + doy
    return era * 146097 + doe - 719468


def civil_from_days(epoch_day: int) -> tuple[int, int, int]:
    """Inverse of :func:`days_from_civil`."""
    z = epoch_day + 719468
    era = z // 146097
    doe = z - era * 146097
    yoe = (doe - doe // 1460 + doe // 36524 - doe // 146096) // 365
    y = yoe + era * 400
    doy = doe - (365 * yoe + yoe // 4 - yoe // 100)
    mp = (5 * doy + 2) // 153
    d = doy - (153 * mp + 2) // 5 + 1
    m = mp + 3 if mp < 10 else mp - 9
    return (y + (1 if m <= 2 else 0), m, d)


@dataclass(frozen=True)
class Duration:
    """A fixed amount of elapsed time, in nanoseconds."""

    nanos: int

    @classmethod
    def of(
        cls,
        *,
        days: int = 0,
        hours: int = 0,
        minutes: int = 0,
        seconds: int = 0,
        nanoseconds: int = 0,
    ) -> "Duration":
        return cls(
            days * NANOS_PER_DAY
            + hours * NANOS_PER_HOUR
            + minutes * NANOS_PER_MINUTE
            + seconds * NANOS_PER_SECOND
            + nanoseconds
        )

    def __add__(self, other: "Duration") -> "Duration":
        if not isinstance(other, Duration):
            return NotImplemented
        return Duration(self.nanos + other.nanos)

    def __sub__(self, other: "Duration") -> "Duration":
        if not isinstance(other, Duration):
            return NotImplemented
        return Duration(self.nanos - other.nanos)

    def __neg__(self) -> "Duration":
        return Duration(-self.nanos)

    def __abs__(self) -> "Duration":
        return Duration(abs(self.nanos))

    def format_offset(self) -> str:
        """Render as a UTC offset string, e.g. ``+02:00`` or ``-05:30``."""
        sign = "+" if self.nanos >= 0 else "-"
        total_seconds = abs(self.nanos) // NANOS_PER_SECOND
        hours, rem = divmod(total_seconds, 3600)
        minutes, seconds = divmod(rem, 60)
        text = f"{sign}{hours:02d}:{minutes:02d}"
        if seconds:
            text += f":{seconds:02d}"
        return text


@dataclass(frozen=True, order=True)
class Instant:
    """A point on the UTC timeline: nanoseconds since 1970-01-01T00:00Z."""

    epoch_nanos: int

    @classmethod
    def from_unix(cls, seconds: int = 0, nanos: int = 0) -> "Instant":
        return cls(seconds * NANOS_PER_SECOND + nanos)

    def plus(self, duration: Duration) -> "Instant":
        return Instant(self.epoch_nanos + duration.nanos)

    def minus(self, duration: Duration) -> "Instant":
        return Instant(self.epoch_nanos - duration.nanos)

    def duration_until(self, other: "Instant") -> Duration:
        return Duration(other.epoch_nanos - self.epoch_nanos)

    def to_wall(self) -> "WallClock":
        """Reinterpret this instant's fields as a wall-clock reading."""
        day, nod = divmod(self.epoch_nanos, NANOS_PER_DAY)
        return WallClock(day, nod)

    def __str__(self) -> str:
        return f"{self.to_wall()}Z"


@dataclass(frozen=True, order=True)
class WallClock:
    """A local date + time-of-day with nanosecond precision, no offset."""

    epoch_day: int
    nano_of_day: int = 0

    def __post_init__(self) -> None:
        if not 0 <= self.nano_of_day < NANOS_PER_DAY:
            raise ValueError(f"nano_of_day out of range: {self.nano_of_day}")

    @classmethod
    def of(
        cls,
        year: int,
        month: int,
        day: int,
        hour: int = 0,
        minute: int = 0,
        second: int = 0,
        nanosecond: int = 0,
    ) -> "WallClock":
        if not 1 <= month <= 12:
            raise ValueError(f"month out of range: {month}")
        if not 1 <= day <= days_in_month(year, month):
            raise ValueError(f"day out of range: {year}-{month}-{day}")
        if not 0 <= hour < 24 or not 0 <= minute < 60 or not 0 <= second < 60:
            raise ValueError("time-of-day out of range")
        if not 0 <= nanosecond < NANOS_PER_SECOND:
            raise ValueError(f"nanosecond out of range: {nanosecond}")
        nod = (
            hour * NANOS_PER_HOUR
            + minute * NANOS_PER_MINUTE
            + second * NANOS_PER_SECOND
            + nanosecond
        )
        return cls(days_from_civil(year, month, day), nod)

    def to_ymd(self) -> tuple[int, int, int]:
        return civil_from_days(self.epoch_day)

    def to_time(self) -> tuple[int, int, int, int]:
        hour, rem = divmod(self.nano_of_day, NANOS_PER_HOUR)
        minute, rem = divmod(rem, NANOS_PER_MINUTE)
        second, nano = divmod(rem, NANOS_PER_SECOND)
        return hour, minute, second, nano

    def weekday(self) -> int:
        """0 = Monday ... 6 = Sunday (1970-01-01 was a Thursday)."""
        return (self.epoch_day + 3) % 7

    # -- calendar stepping (NOT fixed durations) --------------------------

    def plus_days(self, days: int) -> "WallClock":
        return WallClock(self.epoch_day + days, self.nano_of_day)

    def plus_months(self, months: int) -> "WallClock":
        year, month, day = self.to_ymd()
        total = year * 12 + (month - 1) + months
        new_year, new_month0 = divmod(total, 12)
        new_month = new_month0 + 1
        new_day = min(day, days_in_month(new_year, new_month))
        hour, minute, second, nano = self.to_time()
        return WallClock.of(new_year, new_month, new_day, hour, minute, second, nano)

    def plus_years(self, years: int) -> "WallClock":
        return self.plus_months(years * 12)

    # -- elapsed arithmetic ------------------------------------------------

    def plus(self, duration: Duration) -> "WallClock":
        day, nod = divmod(
            self.epoch_day * NANOS_PER_DAY + self.nano_of_day + duration.nanos,
            NANOS_PER_DAY,
        )
        return WallClock(day, nod)

    def minus(self, duration: Duration) -> "WallClock":
        return self.plus(-duration)

    def __sub__(self, other):
        if isinstance(other, WallClock):
            return Duration(
                (self.epoch_day - other.epoch_day) * NANOS_PER_DAY
                + (self.nano_of_day - other.nano_of_day)
            )
        if isinstance(other, Duration):
            return self.minus(other)
        return NotImplemented

    def to_instant_equiv(self) -> Instant:
        """Reinterpret this wall reading as timeline nanoseconds (no offset)."""
        return Instant(self.epoch_day * NANOS_PER_DAY + self.nano_of_day)

    def __str__(self) -> str:
        year, month, day = self.to_ymd()
        hour, minute, second, nano = self.to_time()
        text = f"{year:04d}-{month:02d}-{day:02d}T{hour:02d}:{minute:02d}:{second:02d}"
        if nano:
            text += f".{nano:09d}".rstrip("0")
        return text
