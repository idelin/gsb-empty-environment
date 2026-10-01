"""Shared helpers: synthetic offset tables and exact UTC constructors."""
from fractions import Fraction

from calpart import Instant, OffsetTable


def U(year, month, day, hour=0, minute=0, second=0, fraction=0):
    """Exact UTC seconds for a civil date-time."""
    return Instant.from_civil(year, month, day, hour, minute, second, fraction).seconds


def europe_like():
    """Synthetic CET/CEST-like table: base +1h, DST +2h between the last
    Sundays of March and October (01:00 UTC switch), 2023-2025."""
    transitions = []
    for year, march_day, october_day in ((2023, 26, 29), (2024, 31, 27), (2025, 30, 26)):
        transitions.append((U(year, 3, march_day, 1), 7200))
        transitions.append((U(year, 10, october_day, 1), 3600))
    return OffsetTable(3600, transitions)


def repeat_hour_table():
    """Local 08:00-10:00 repeats once: offset drops 2h at 10:00 UTC, returns
    at 12:00 UTC (skipping local 10:00-12:00 on the way back)."""
    return OffsetTable(0, [(U(2024, 1, 2, 10), -7200), (U(2024, 1, 2, 12), 0)])


def wild_jump_table():
    """A 30-hour backward jump: local 2024-01-02 ends up with two disjoint
    UTC preimages."""
    return OffsetTable(0, [(U(2024, 1, 2, 12), -108000), (U(2024, 1, 4, 0), 0)])
