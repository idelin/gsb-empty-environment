from __future__ import annotations

from dataclasses import dataclass
from typing import Generic, TypeVar


NANOS_PER_SECOND = 1_000_000_000
SECONDS_PER_DAY = 86_400
NANOS_PER_DAY = SECONDS_PER_DAY * NANOS_PER_SECOND


def days_in_month(year: int, month: int) -> int:
    if month == 2:
        return 29 if is_leap_year(year) else 28
    if month in (4, 6, 9, 11):
        return 30
    return 31


def is_leap_year(year: int) -> bool:
    return year % 4 == 0 and (year % 100 != 0 or year % 400 == 0)


def days_from_civil(year: int, month: int, day: int) -> int:
    adjusted_year = year - (1 if month <= 2 else 0)
    era = adjusted_year // 400
    year_of_era = adjusted_year - era * 400
    month_ordinal = (153 * (month + (-3 if month > 2 else 9)) + 2) // 5
    day_of_year = month_ordinal + day - 1
    day_of_era = (
        year_of_era * 365
        + year_of_era // 4
        - year_of_era // 100
        + day_of_year
    )
    return era * 146_097 + day_of_era - 719_468


def civil_from_days(days: int) -> tuple[int, int, int]:
    shifted = days + 719_468
    era = shifted // 146_097
    day_of_era = shifted - era * 146_097
    year_of_era = (
        day_of_era - day_of_era // 1_460 + day_of_era // 36_524 - day_of_era // 146_096
    ) // 365
    year = year_of_era + era * 400
    day_of_year = day_of_era - (
        365 * year_of_era + year_of_era // 4 - year_of_era // 100
    )
    month_primitive = (5 * day_of_year + 2) // 153
    day = day_of_year - (153 * month_primitive + 2) // 5 + 1
    month = month_primitive + (3 if month_primitive < 10 else -9)
    if month <= 2:
        year += 1
    return year, month, day


def add_calendar_months(year: int, month: int, day: int, months: int) -> tuple[int, int, int]:
    month_index = (year * 12 + month - 1) + months
    new_year, zero_based_month = divmod(month_index, 12)
    new_month = zero_based_month + 1
    return new_year, new_month, min(day, days_in_month(new_year, new_month))


def add_calendar_years(year: int, month: int, day: int, years: int) -> tuple[int, int, int]:
    return add_calendar_months(year, month, day, years * 12)


@dataclass(frozen=True, slots=True, order=True)
class Duration:
    nanoseconds: int

    @classmethod
    def seconds(cls, seconds: int) -> Duration:
        return cls(int(seconds) * NANOS_PER_SECOND)

    @classmethod
    def hours(cls, hours: int) -> Duration:
        return cls.seconds(int(hours) * 3_600)

    @classmethod
    def zero(cls) -> Duration:
        return cls(0)

    def __add__(self, other: Duration) -> Duration:
        return Duration(self.nanoseconds + other.nanoseconds)

    def __sub__(self, other: Duration) -> Duration:
        return Duration(self.nanoseconds - other.nanoseconds)

    def __neg__(self) -> Duration:
        return Duration(-self.nanoseconds)

    def to_wall(self) -> WallClock:
        days, remainder = divmod(self.nanoseconds, NANOS_PER_DAY)
        year, month, day = civil_from_days(days)
        return WallClock.from_components(year, month, day, nanoseconds_within_day=remainder)


@dataclass(frozen=True, slots=True, order=True)
class Instant:
    unix_nanoseconds: int

    @classmethod
    def from_unix_seconds(cls, seconds: int, nanoseconds: int = 0) -> Instant:
        return cls(int(seconds) * NANOS_PER_SECOND + int(nanoseconds))

    @classmethod
    def epoch(cls) -> Instant:
        return cls(0)

    def __add__(self, duration: Duration) -> Instant:
        return Instant(self.unix_nanoseconds + duration.nanoseconds)

    def __sub__(self, other: Instant | Duration) -> Instant | Duration:
        if isinstance(other, Duration):
            return Instant(self.unix_nanoseconds - other.nanoseconds)
        return Duration(self.unix_nanoseconds - other.unix_nanoseconds)


@dataclass(frozen=True, slots=True, order=True)
class WallClock:
    civil_days: int
    nanoseconds_within_day: int

    def __post_init__(self) -> None:
        if not 0 <= self.nanoseconds_within_day < NANOS_PER_DAY:
            raise ValueError("nanoseconds_within_day must be in [0, 86400000000000)")

    @classmethod
    def from_components(
        cls,
        year: int,
        month: int,
        day: int,
        hour: int = 0,
        minute: int = 0,
        second: int = 0,
        nanoseconds_within_second: int = 0,
        nanoseconds_within_day: int | None = None,
    ) -> WallClock:
        if not 1 <= month <= 12:
            raise ValueError("month must be in [1, 12]")
        if not 1 <= day <= days_in_month(year, month):
            raise ValueError("day is outside the month")
        for name, value, limit in (
            ("hour", hour, 24),
            ("minute", minute, 60),
            ("second", second, 60),
            ("nanoseconds_within_second", nanoseconds_within_second, NANOS_PER_SECOND),
        ):
            if not 0 <= value < limit:
                raise ValueError(f"{name} must be in [0, {limit})")
        if nanoseconds_within_day is None:
            nanoseconds_within_day = (
                ((hour * 60 + minute) * 60 + second) * NANOS_PER_SECOND
                + nanoseconds_within_second
            )
        if not 0 <= nanoseconds_within_day < NANOS_PER_DAY:
            raise ValueError("time of day must be in [0, 24:00)")
        return cls(days_from_civil(year, month, day), nanoseconds_within_day)

    @property
    def date(self) -> tuple[int, int, int]:
        return civil_from_days(self.civil_days)

    @property
    def year(self) -> int:
        return self.date[0]

    @property
    def month(self) -> int:
        return self.date[1]

    @property
    def day(self) -> int:
        return self.date[2]

    @property
    def time_of_day(self) -> tuple[int, int, int, int]:
        seconds, nano = divmod(self.nanoseconds_within_day, NANOS_PER_SECOND)
        hour, remainder = divmod(seconds, 3_600)
        minute, second = divmod(remainder, 60)
        return hour, minute, second, nano

    @property
    def naive_nanoseconds(self) -> int:
        return self.civil_days * NANOS_PER_DAY + self.nanoseconds_within_day

    def add_calendar_days(self, days: int) -> WallClock:
        return WallClock(self.civil_days + int(days), self.nanoseconds_within_day)

    def add_calendar_months(self, months: int) -> WallClock:
        year, month, day = add_calendar_months(self.year, self.month, self.day, months)
        hour, minute, second, nano = self.time_of_day
        return WallClock.from_components(year, month, day, hour, minute, second, nano)

    def add_calendar_years(self, years: int) -> WallClock:
        return self.add_calendar_months(years * 12)

    def replace_day(self, day: int) -> WallClock:
        year, month, _ = self.date
        hour, minute, second, nano = self.time_of_day
        return WallClock.from_components(
            year,
            month,
            min(day, days_in_month(year, month)),
            hour,
            minute,
            second,
            nano,
        )

    def isoformat(self) -> str:
        year, month, day = self.date
        hour, minute, second, nano = self.time_of_day
        sign = "-" if year < 0 else ""
        year_abs = abs(year)
        return (
            f"{sign}{year_abs:04d}-{month:02d}-{day:02d}"
            f"T{hour:02d}:{minute:02d}:{second:02d}.{nano:09d}"
        )

    def __str__(self) -> str:
        return self.isoformat()


T = TypeVar("T", Instant, WallClock)


@dataclass(frozen=True, slots=True)
class Interval(Generic[T]):
    start: T
    end: T

    def __post_init__(self) -> None:
        if self.end < self.start:
            raise ValueError("interval end must not precede start")

    def contains(self, value: T) -> bool:
        return self.start <= value < self.end

    @property
    def empty(self) -> bool:
        return self.start == self.end


@dataclass(frozen=True, slots=True)
class InstantInterval(Interval[Instant]):
    @property
    def duration(self) -> Duration:
        return Duration(self.end.unix_nanoseconds - self.start.unix_nanoseconds)


@dataclass(frozen=True, slots=True)
class WallInterval(Interval[WallClock]):
    @property
    def wall_nanoseconds(self) -> int:
        return self.end.naive_nanoseconds - self.start.naive_nanoseconds


def merge_intervals(intervals: list[Interval[T]]) -> list[Interval[T]]:
    ordered = sorted(intervals, key=lambda item: (item.start, item.end))
    merged: list[Interval[T]] = []
    for interval in ordered:
        if not merged or interval.start > merged[-1].end:
            merged.append(interval)
        elif interval.end > merged[-1].end:
            merged[-1] = type(interval)(merged[-1].start, interval.end)
    return merged
