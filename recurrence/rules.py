"""Recurrence rules over wall-clock time.

Rules generate occurrences lazily inside a half-open query window
``[start, end)``; nothing is ever enumerated without a window. Counting
and k-th occurrence lookup use arithmetic jumps instead of per-day scans:

- ``DailyRule``:   O(1) count / O(1) kth.
- ``WeeklyRule``:  O(1) count (boundary weeks only) / O(log n) kth.
- ``MonthlyRule``: O(months) count (never per-day) / O(log n) kth;
  months without the requested day (e.g. the 31st) are skipped, and
  advancement is by calendar months of wall time, never fixed seconds.

Each rule records statistics about its last ``count_between`` call in
``last_meta`` (e.g. how many periods were jumped arithmetically), which
tests cross-check against a brute-force per-day reference.
"""

from __future__ import annotations

from abc import ABC, abstractmethod
from calendar import monthrange
from datetime import datetime
from typing import Iterator

from .timeprims import SECONDS_PER_DAY, WallClock

_NAIVE_EPOCH = datetime(1970, 1, 1)


def _ceil_div(a: int, b: int) -> int:
    return -(-a // b)


def _day_index(wall_seconds: int) -> int:
    """Floor day index of a naive-epoch-seconds value."""
    return wall_seconds // SECONDS_PER_DAY


class Rule(ABC):
    """An infinite wall-clock recurrence anchored at ``anchor`` (inclusive)."""

    anchor: WallClock

    def __init__(self) -> None:
        self.last_meta: dict = {}

    @abstractmethod
    def iter_wall(self, start: WallClock, end: WallClock) -> Iterator[WallClock]:
        """Yield occurrences in the half-open window [start, end), ascending."""

    @abstractmethod
    def count_between(self, start: WallClock, end: WallClock) -> int:
        """Number of occurrences in [start, end), computed without per-day scans."""

    @abstractmethod
    def contains(self, wall: WallClock) -> bool:
        """True iff ``wall`` is exactly an occurrence."""

    @abstractmethod
    def spec(self) -> tuple:
        """Canonical, hashable description used for fingerprints."""

    # -- period-based skeleton shared by kth() ------------------------------
    @abstractmethod
    def _period_start_sec(self, j: int) -> int:
        """Naive seconds at which period j begins (periods tile the rule)."""

    @abstractmethod
    def _period_occurrences(self, j: int) -> list[int]:
        """Sorted occurrence times (naive seconds) inside period j."""

    def _count_periods_before(self, j: int) -> int:
        if j <= 0:
            return 0
        return self.count_between(
            self.anchor, WallClock.from_seconds(self._period_start_sec(j))
        )

    def kth(self, k: int) -> WallClock:
        """Return the k-th occurrence (0-based) at or after the anchor."""
        if k < 0:
            raise IndexError("k must be >= 0")
        lo, hi = 0, 1
        while self._count_periods_before(hi) <= k:
            hi *= 2
        while lo + 1 < hi:
            mid = (lo + hi) // 2
            if self._count_periods_before(mid) > k:
                hi = mid
            else:
                lo = mid
        before = self._count_periods_before(lo)
        occs = self._period_occurrences(lo)
        return WallClock.from_seconds(occs[k - before])


class DailyRule(Rule):
    """Every ``interval`` days at the anchor's time of day."""

    def __init__(self, anchor: WallClock, interval: int = 1):
        super().__init__()
        if interval < 1:
            raise ValueError("interval must be >= 1")
        self.anchor = anchor
        self.interval = interval
        self._t0 = anchor.seconds
        self._step = interval * SECONDS_PER_DAY

    def _idx_ceil(self, t: int) -> int:
        return max(0, _ceil_div(t - self._t0, self._step))

    def count_between(self, start: WallClock, end: WallClock) -> int:
        lo = self._idx_ceil(start.seconds)
        hi = self._idx_ceil(end.seconds)
        n = max(0, hi - lo)
        self.last_meta = {"periods_jumped": n, "periods_scanned": 0}
        return n

    def iter_wall(self, start: WallClock, end: WallClock) -> Iterator[WallClock]:
        for i in range(self._idx_ceil(start.seconds), self._idx_ceil(end.seconds)):
            yield WallClock.from_seconds(self._t0 + i * self._step)

    def contains(self, wall: WallClock) -> bool:
        d = wall.seconds - self._t0
        return d >= 0 and d % self._step == 0

    def kth(self, k: int) -> WallClock:
        if k < 0:
            raise IndexError("k must be >= 0")
        return WallClock.from_seconds(self._t0 + k * self._step)

    def _period_start_sec(self, j: int) -> int:
        return self._t0 + j * self._step

    def _period_occurrences(self, j: int) -> list[int]:
        return [self._t0 + j * self._step]

    def spec(self) -> tuple:
        return ("daily", self._t0, self.interval)


class WeeklyRule(Rule):
    """Selected weekdays (0=Mon..6=Sun) every ``interval`` weeks."""

    def __init__(self, anchor: WallClock, weekdays, interval: int = 1):
        super().__init__()
        weekdays = tuple(sorted(set(weekdays)))
        if not weekdays or any(wd < 0 or wd > 6 for wd in weekdays):
            raise ValueError("weekdays must be a non-empty subset of 0..6")
        if interval < 1:
            raise ValueError("interval must be >= 1")
        self.anchor = anchor
        self.weekdays = weekdays
        self.interval = interval
        self._t0 = anchor.seconds
        self._tod = anchor.dt.hour * 3600 + anchor.dt.minute * 60 + anchor.dt.second
        monday_ord = anchor.dt.toordinal() - anchor.dt.weekday()
        self._week0_day = monday_ord - _NAIVE_EPOCH.toordinal()
        self._week0_sec = self._week0_day * SECONDS_PER_DAY
        self._step_days = 7 * interval
        self._step_sec = self._step_days * SECONDS_PER_DAY

    def _occ_sec(self, j: int, wd: int) -> int:
        return self._week0_sec + j * self._step_sec + wd * SECONDS_PER_DAY + self._tod

    def _week_range(self, a_t: int, b_t: int):
        """Week indices [j_lo, j_hi) whose 7-day span intersects [a_t, b_t)."""
        first_day = _day_index(a_t)
        last_day = _ceil_div(b_t, SECONDS_PER_DAY) - 1
        j_lo = max(0, _ceil_div(first_day - self._week0_day - 6, self._step_days))
        j_hi = max(0, _ceil_div(last_day - self._week0_day + 1, self._step_days))
        return j_lo, max(j_lo, j_hi)

    def _week_count(self, j: int, lo_t: int, hi_t: int) -> int:
        return sum(
            1
            for wd in self.weekdays
            if lo_t <= self._occ_sec(j, wd) < hi_t and self._occ_sec(j, wd) >= self._t0
        )

    def count_between(self, start: WallClock, end: WallClock) -> int:
        a_t = max(start.seconds, self._t0)
        b_t = end.seconds
        if b_t <= a_t:
            self.last_meta = {"periods_jumped": 0, "periods_scanned": 0}
            return 0
        j_lo, j_hi = self._week_range(a_t, b_t)
        if j_hi - j_lo == 1:
            total = self._week_count(j_lo, a_t, b_t)
            scanned = 1
        else:
            total = (
                self._week_count(j_lo, a_t, 1 << 62)
                + self._week_count(j_hi - 1, -(1 << 62), b_t)
                + (j_hi - j_lo - 2) * len(self.weekdays)
            )
            scanned = 2
        self.last_meta = {
            "periods_jumped": max(0, j_hi - j_lo - scanned),
            "periods_scanned": scanned,
        }
        return total

    def iter_wall(self, start: WallClock, end: WallClock) -> Iterator[WallClock]:
        a_t = max(start.seconds, self._t0)
        b_t = end.seconds
        if b_t <= a_t:
            return
        j_lo, j_hi = self._week_range(a_t, b_t)
        for j in range(j_lo, j_hi):
            for wd in self.weekdays:
                t = self._occ_sec(j, wd)
                if a_t <= t < b_t and t >= self._t0:
                    yield WallClock.from_seconds(t)

    def contains(self, wall: WallClock) -> bool:
        t = wall.seconds
        if t < self._t0:
            return False
        tod = wall.dt.hour * 3600 + wall.dt.minute * 60 + wall.dt.second
        if tod != self._tod:
            return False
        weekday = wall.dt.weekday()
        if weekday not in self.weekdays:
            return False
        day = wall.dt.toordinal() - _NAIVE_EPOCH.toordinal()
        monday_of_week = day - weekday
        delta_days = monday_of_week - self._week0_day
        return delta_days >= 0 and delta_days % self._step_days == 0

    def _period_start_sec(self, j: int) -> int:
        return self._week0_sec + j * self._step_sec

    def _period_occurrences(self, j: int) -> list[int]:
        return [
            t
            for t in (self._occ_sec(j, wd) for wd in self.weekdays)
            if t >= self._t0
        ]

    def spec(self) -> tuple:
        return ("weekly", self._t0, self.weekdays, self.interval)


class MonthlyRule(Rule):
    """Selected days of month (1..31) every ``interval`` months.

    A day that does not exist in a given month (e.g. the 31st in
    February) is skipped for that month; the rule never falls back to
    another day and never advances by a fixed number of seconds.
    """

    def __init__(self, anchor: WallClock, monthdays, interval: int = 1):
        super().__init__()
        monthdays = tuple(sorted(set(monthdays)))
        if not monthdays or any(d < 1 or d > 31 for d in monthdays):
            raise ValueError("monthdays must be a non-empty subset of 1..31")
        if interval < 1:
            raise ValueError("interval must be >= 1")
        self.anchor = anchor
        self.monthdays = monthdays
        self.interval = interval
        self._t0 = anchor.seconds
        self._tod = anchor.dt.hour * 3600 + anchor.dt.minute * 60 + anchor.dt.second
        self._m0 = anchor.dt.year * 12 + (anchor.dt.month - 1)

    def _month_index_range(self, a_t: int, b_t: int):
        a = WallClock.from_seconds(a_t).dt
        b = WallClock.from_seconds(b_t - 1).dt  # b is exclusive
        a_m = a.year * 12 + (a.month - 1)
        b_m = b.year * 12 + (b.month - 1)
        j_lo = max(0, _ceil_div(a_m - self._m0, self.interval))
        j_hi = max(0, _ceil_div(b_m - self._m0 + 1, self.interval))
        return j_lo, max(j_lo, j_hi)

    def _occurrences_in_month(self, j: int) -> list[int]:
        m = self._m0 + j * self.interval
        year, month0 = divmod(m, 12)
        month = month0 + 1
        dim = monthrange(year, month)[1]
        out = []
        for d in self.monthdays:
            if d <= dim:
                t = (
                    datetime(year, month, d) - _NAIVE_EPOCH
                ).days * SECONDS_PER_DAY + self._tod
                if t >= self._t0:
                    out.append(t)
        return sorted(out)

    def count_between(self, start: WallClock, end: WallClock) -> int:
        a_t = max(start.seconds, self._t0)
        b_t = end.seconds
        if b_t <= a_t:
            self.last_meta = {"periods_jumped": 0, "periods_scanned": 0}
            return 0
        j_lo, j_hi = self._month_index_range(a_t, b_t)
        total = 0
        for j in range(j_lo, j_hi):
            for t in self._occurrences_in_month(j):
                if a_t <= t < b_t:
                    total += 1
        self.last_meta = {"periods_jumped": 0, "periods_scanned": j_hi - j_lo}
        return total

    def iter_wall(self, start: WallClock, end: WallClock) -> Iterator[WallClock]:
        a_t = max(start.seconds, self._t0)
        b_t = end.seconds
        if b_t <= a_t:
            return
        j_lo, j_hi = self._month_index_range(a_t, b_t)
        for j in range(j_lo, j_hi):
            for t in self._occurrences_in_month(j):
                if a_t <= t < b_t:
                    yield WallClock.from_seconds(t)

    def contains(self, wall: WallClock) -> bool:
        t = wall.seconds
        if t < self._t0:
            return False
        tod = wall.dt.hour * 3600 + wall.dt.minute * 60 + wall.dt.second
        if tod != self._tod or wall.dt.day not in self.monthdays:
            return False
        m = wall.dt.year * 12 + (wall.dt.month - 1)
        return m >= self._m0 and (m - self._m0) % self.interval == 0

    def _period_start_sec(self, j: int) -> int:
        m = self._m0 + j * self.interval
        year, month0 = divmod(m, 12)
        return (datetime(year, month0 + 1, 1) - _NAIVE_EPOCH).days * SECONDS_PER_DAY

    def _period_occurrences(self, j: int) -> list[int]:
        return self._occurrences_in_month(j)

    def spec(self) -> tuple:
        return ("monthly", self._t0, self.monthdays, self.interval)
