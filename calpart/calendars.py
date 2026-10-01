"""Local calendar partitions: civil days, weeks (custom start), months.

Each calendar maps a :class:`~calpart.exact.WallTime` to a label and can
enumerate its boundary wall-times by *jumping* from boundary to boundary
(calendar arithmetic), never by scanning seconds.
"""

from __future__ import annotations

from datetime import timedelta
from typing import Iterator

from .exact import WallTime


class DayCalendar:
    name = "day"

    def floor(self, w: WallTime) -> WallTime:
        return WallTime(w.day, 0)

    def next_boundary(self, w: WallTime) -> WallTime:
        f = self.floor(w)
        if f > w:
            return f
        return WallTime(f.day + timedelta(days=1), 0)

    def label(self, w: WallTime) -> str:
        return w.day.isoformat()


class WeekCalendar:
    """Weeks starting on ``week_start`` (0=Monday ... 6=Sunday)."""

    def __init__(self, week_start: int = 0):
        if not 0 <= week_start <= 6:
            raise ValueError("week_start must be in 0..6 (Mon..Sun)")
        self.week_start = week_start
        self.name = f"week(start={week_start})"

    def floor(self, w: WallTime) -> WallTime:
        back = (w.day.weekday() - self.week_start) % 7
        return WallTime(w.day - timedelta(days=back), 0)

    def next_boundary(self, w: WallTime) -> WallTime:
        f = self.floor(w)
        if f > w:
            return f
        return WallTime(f.day + timedelta(days=7), 0)

    def label(self, w: WallTime) -> str:
        return self.floor(w).day.isoformat()


class MonthCalendar:
    name = "month"

    def floor(self, w: WallTime) -> WallTime:
        return WallTime(w.day.replace(day=1), 0)

    def next_boundary(self, w: WallTime) -> WallTime:
        f = self.floor(w)
        if f > w:
            return f
        y, m = f.day.year, f.day.month
        if m == 12:
            nxt = f.day.replace(year=y + 1, month=1)
        else:
            nxt = f.day.replace(month=m + 1)
        return WallTime(nxt, 0)

    def label(self, w: WallTime) -> str:
        return f"{w.day.year:04d}-{w.day.month:02d}"


def iter_boundaries(cal, wall_lo: WallTime, wall_hi: WallTime, work=None) -> Iterator[WallTime]:
    """Yield boundaries ``b`` with ``wall_lo < b <= wall_hi`` by calendar jumps.

    ``work`` (optional) is a counter object; each emitted boundary increments
    ``work.boundary_jumps`` so callers can measure jump-based effort instead
    of per-second scanning.
    """
    b = cal.next_boundary(wall_lo)
    while b <= wall_hi:
        if work is not None:
            work.boundary_jumps += 1
        yield b
        b = cal.next_boundary(b)
