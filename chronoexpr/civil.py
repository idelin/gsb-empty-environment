"""Proleptic Gregorian calendar arithmetic (exact, integer only)."""

from __future__ import annotations

_MDAYS = (31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31)


def is_leap_year(year: int) -> bool:
    return year % 4 == 0 and (year % 100 != 0 or year % 400 == 0)


def days_in_month(year: int, month: int) -> int:
    if not 1 <= month <= 12:
        raise ValueError(f"month {month} out of range 1..12")
    if month == 2 and is_leap_year(year):
        return 29
    return _MDAYS[month - 1]


def days_from_civil(year: int, month: int, day: int) -> int:
    """Serial day number with 1970-01-01 == 0 (Howard Hinnant's algorithm)."""
    year -= month <= 2
    era = (year if year >= 0 else year - 399) // 400
    yoe = year - era * 400
    mp = (month + 9) % 12
    doy = (153 * mp + 2) // 5 + day - 1
    doe = yoe * 365 + yoe // 4 - yoe // 100 + doy
    return era * 146097 + doe - 719468


def civil_from_days(serial: int) -> tuple[int, int, int]:
    """Inverse of days_from_civil."""
    serial += 719468
    era = (serial if serial >= 0 else serial - 146096) // 146097
    doe = serial - era * 146097
    yoe = (doe - doe // 1460 + doe // 36524 - doe // 146096) // 365
    year = yoe + era * 400
    doy = doe - (365 * yoe + yoe // 4 - yoe // 100)
    mp = (5 * doy + 2) // 153
    day = doy - (153 * mp + 2) // 5 + 1
    month = mp + 3 if mp < 10 else mp - 9
    return year + (month <= 2), month, day
