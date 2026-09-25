"""Proleptic Gregorian calendar arithmetic with month-end clamping."""
import datetime as dt


def days_in_month(year: int, month: int) -> int:
    if month == 12:
        nxt = dt.date(year + 1, 1, 1)
    else:
        nxt = dt.date(year, month + 1, 1)
    return (nxt - dt.date(year, month, 1)).days


def add_months(date: dt.date, months: int) -> dt.date:
    """Add calendar months, clamping the day to the target month's end.

    Clamping is deliberately *not* reversible: 2024-01-31 + 1mo ->
    2024-02-29, and + 1mo again -> 2024-03-29 (not 2024-03-31).
    """
    total = date.year * 12 + (date.month - 1) + months
    year, month0 = divmod(total, 12)
    month = month0 + 1
    day = min(date.day, days_in_month(year, month))
    return dt.date(year, month, day)
