"""Local calendar boundary grids: day, week (custom start) and month.

A grid partitions wall-clock time into periods. All boundary arithmetic is
exact (``Fraction`` seconds on the wall-clock axis) and supports jumping
whole periods arithmetically, which is what makes enormous windows cheap.
"""
from __future__ import annotations

from .exact import (
    EPOCH_WEEKDAY,
    SECONDS_PER_DAY,
    civil_from_days,
    days_from_civil,
    wall_seconds,
)

DAY_NAMES = ("Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun")


def _date_label(days: int) -> str:
    year, month, day = civil_from_days(days)
    return f"{year:04d}-{month:02d}-{day:02d}"


class DayGrid:
    """Local calendar days, boundaries at local midnight."""

    name = "day"
    length = SECONDS_PER_DAY

    def floor(self, w):
        w = wall_seconds(w)
        return (w // SECONDS_PER_DAY) * SECONDS_PER_DAY

    def is_boundary(self, w):
        return wall_seconds(w) % SECONDS_PER_DAY == 0

    def next_boundary(self, w):
        return self.floor(w) + SECONDS_PER_DAY

    def advance(self, boundary, count):
        return wall_seconds(boundary) + count * SECONDS_PER_DAY

    def count_between(self, boundary_a, boundary_b):
        span = wall_seconds(boundary_b) - wall_seconds(boundary_a)
        return int(span // SECONDS_PER_DAY)

    def label(self, boundary):
        days = int(wall_seconds(boundary) // SECONDS_PER_DAY)
        return _date_label(days)


class WeekGrid:
    """Local weeks starting on a configurable weekday (0 == Monday)."""

    name = "week"
    length = 7 * SECONDS_PER_DAY

    def __init__(self, week_start=0):
        if not 0 <= week_start <= 6:
            raise ValueError("week_start must be in 0..6 (Monday..Sunday)")
        self.week_start = week_start

    def floor(self, w):
        days = wall_seconds(w) // SECONDS_PER_DAY
        weekday = (EPOCH_WEEKDAY + days) % 7
        start_days = days - (weekday - self.week_start) % 7
        return start_days * SECONDS_PER_DAY

    def is_boundary(self, w):
        w = wall_seconds(w)
        return w == self.floor(w)

    def next_boundary(self, w):
        return self.floor(w) + self.length

    def advance(self, boundary, count):
        return wall_seconds(boundary) + count * self.length

    def count_between(self, boundary_a, boundary_b):
        span = wall_seconds(boundary_b) - wall_seconds(boundary_a)
        return int(span // self.length)

    def label(self, boundary):
        days = int(wall_seconds(boundary) // SECONDS_PER_DAY)
        return f"week[{DAY_NAMES[self.week_start]}]@{_date_label(days)}"


class MonthGrid:
    """Local calendar months, boundaries on the first of each month.

    Month lengths follow the proleptic Gregorian calendar, so December rolls
    into January of the next year without dropping any period.
    """

    name = "month"

    def _civil(self, w):
        days = int(wall_seconds(w) // SECONDS_PER_DAY)
        year, month, _ = civil_from_days(days)
        return year, month

    @staticmethod
    def _boundary(year, month):
        return days_from_civil(year, month, 1) * SECONDS_PER_DAY

    def floor(self, w):
        year, month = self._civil(w)
        return self._boundary(year, month)

    def is_boundary(self, w):
        return wall_seconds(w) == self.floor(w)

    def next_boundary(self, w):
        year, month = self._civil(self.floor(w))
        if month == 12:
            year, month = year + 1, 1
        else:
            month += 1
        return self._boundary(year, month)

    def advance(self, boundary, count):
        year, month = self._civil(boundary)
        total = year * 12 + (month - 1) + count
        return self._boundary(total // 12, total % 12 + 1)

    def count_between(self, boundary_a, boundary_b):
        ya, ma = self._civil(boundary_a)
        yb, mb = self._civil(boundary_b)
        return (yb - ya) * 12 + (mb - ma)

    def label(self, boundary):
        year, month = self._civil(boundary)
        return f"{year:04d}-{month:02d}"
