"""Recurrence rules and single-rule schedules.

Three frequencies are supported, each aligned to the *calendar* rather than
to a fixed number of seconds:

* :class:`DailyRule`   -- every ``interval`` days at a time of day;
* :class:`WeeklyRule`  -- every ``interval`` weeks on named weekdays;
* :class:`MonthlyRule` -- every ``interval`` months on numbered month days
  (day 31 simply produces nothing in shorter months).

Expansion never scans every calendar day: generators jump period by period
(day / week / month) and report how many such jumps a query cost.  Excluded
dates (``exdates``) and one-off additions (``rdates``) are merged with an
explicit precedence rule -- exclusion wins on the same wall date.
"""

from __future__ import annotations

import datetime as _dt
import hashlib
import json
from dataclasses import dataclass, field

from .offsets import Ambiguous, Gap, OffsetTable, Resolved
from .time_model import Instant, WallTime

MONDAY, TUESDAY, WEDNESDAY, THURSDAY, FRIDAY, SATURDAY, SUNDAY = range(7)


@dataclass(frozen=True, order=True)
class Event:
    """One occurrence on the absolute time line.

    ``wall`` is the nominal calendar expression (e.g. the scheduled 02:30
    inside a spring-forward gap); ``mapped_wall`` is the wall reading at the
    resolved instant, which differs only when a gap policy moves the event.
    """

    instant: Instant
    wall: WallTime
    offset_us: int
    source: str
    kind: str = "rule"  # "rule" | "extra"
    mapped_wall: WallTime | None = None


@dataclass
class Expansion:
    events: list[Event] = field(default_factory=list)
    jumps: int = 0

    def merge(self, other: "Expansion") -> None:
        self.events.extend(other.events)
        self.jumps += other.jumps


def _month_index(year: int, month: int) -> int:
    return year * 12 + (month - 1)


def _split_month_index(index: int) -> tuple[int, int]:
    year, month = divmod(index, 12)
    return year, month + 1


def _days_in_month(year: int, month: int) -> int:
    if month == 12:
        nxt = _dt.date(year + 1, 1, 1)
    else:
        nxt = _dt.date(year, month + 1, 1)
    return (nxt - _dt.timedelta(days=1)).day


def _canonical_hash(parts: list) -> str:
    payload = json.dumps(parts, separators=(",", ":"), sort_keys=True, default=str)
    return hashlib.sha1(payload.encode("ascii")).hexdigest()[:12]


class Rule:
    """Base class for calendar-aligned recurrence rules."""

    frequency = "none"

    def __init__(
        self,
        start: WallTime,
        *,
        interval: int = 1,
        name: str = "rule",
    ):
        if interval < 1:
            raise ValueError("interval must be >= 1")
        self.start = start
        self.interval = interval
        self.name = name

    @property
    def time_of_day_us(self) -> int:
        return self.start.microsecond

    def fingerprint_parts(self) -> list:
        return [
            self.frequency,
            self.start.isoformat(),
            self.interval,
            self.name,
            list(self._rule_key()),
        ]

    def _rule_key(self):
        return ()

    def period_range(self, lo: _dt.date, hi_exclusive: _dt.date) -> tuple[int, int]:
        """Inclusive indices of recurrence periods overlapping [lo, hi)."""""
        raise NotImplementedError

    def period_events(self, index: int) -> list[tuple[_dt.date, str]]:
        """Candidate ``(date, tag)`` pairs inside one period index."""
        raise NotImplementedError

    def candidate_dates(
        self, lo: _dt.date, hi_exclusive: _dt.date
    ) -> tuple[list[_dt.date], int]:
        """Jump directly to candidate dates in [lo, hi_exclusive).

        Returns the dates and the number of period jumps performed.  This is
        the bounded, non-enumerating core used by schedule expansion.
        """
        first, last = self.period_range(lo, hi_exclusive)
        if first > last:
            return [], 0
        out: list[_dt.date] = []
        for index in range(first, last + 1):
            for date, _tag in self.period_events(index):
                if lo <= date < hi_exclusive and date >= self.start.date:
                    out.append(date)
        return out, last - first + 1


class DailyRule(Rule):
    frequency = "daily"

    def period_range(self, lo, hi_exclusive):
        p0 = self.start.date.toordinal()
        first_index = (lo.toordinal() - p0 + self.interval - 1) // self.interval
        last_index = (hi_exclusive.toordinal() - 1 - p0) // self.interval
        return max(0, first_index), max(-1, last_index)

    def period_events(self, index):
        d = _dt.date.fromordinal(self.start.date.toordinal() + index * self.interval)
        return [(d, "D")]


class WeeklyRule(Rule):
    frequency = "weekly"

    def __init__(self, start, *, by_weekday=(MONDAY,), interval=1, name="rule"):
        super().__init__(start, interval=interval, name=name)
        days = sorted({int(w) for w in by_weekday})
        if not days or any(not 0 <= w <= 6 for w in days):
            raise ValueError("by_weekday must contain weekdays 0..6 (Mon=0)")
        self.by_weekday = tuple(days)

    def _rule_key(self):
        return self.by_weekday

    def _week_monday(self, index: int) -> _dt.date:
        start_monday = self.start.date - _dt.timedelta(days=self.start.date.weekday())
        return start_monday + _dt.timedelta(days=7 * self.interval * index)

    def period_range(self, lo, hi_exclusive):
        start_monday = self.start.date - _dt.timedelta(days=self.start.date.weekday())
        # Week containing hi_exclusive - 1 may hold a candidate < hi_exclusive.
        first_ordinal = lo.toordinal() - lo.weekday()
        last_ordinal = (hi_exclusive - _dt.timedelta(days=1)).toordinal()
        last_ordinal -= _dt.date.fromordinal(last_ordinal).weekday()
        first = (first_ordinal - start_monday.toordinal() + self.interval * 7 - 7) // (
            7 * self.interval
        )
        last = (last_ordinal - start_monday.toordinal()) // (7 * self.interval)
        return max(0, first), max(-1, last)

    def period_events(self, index):
        monday = self._week_monday(index)
        return [(monday + _dt.timedelta(days=w), f"W{w}") for w in self.by_weekday]


class MonthlyRule(Rule):
    frequency = "monthly"

    def __init__(self, start, *, by_monthday=(1,), interval=1, name="rule"):
        super().__init__(start, interval=interval, name=name)
        days = sorted({int(d) for d in by_monthday})
        if not days or any(not 1 <= d <= 31 for d in days):
            raise ValueError("by_monthday must contain days in 1..31")
        self.by_monthday = tuple(days)

    def _rule_key(self):
        return self.by_monthday

    def period_range(self, lo, hi_exclusive):
        p0 = _month_index(self.start.year, self.start.month)
        first = (_month_index(lo.year, lo.month) - p0 + self.interval - 1) // self.interval
        boundary = hi_exclusive - _dt.timedelta(days=1)  # month of the last inclusive day
        last = (_month_index(boundary.year, boundary.month) - p0) // self.interval
        return max(0, first), max(-1, last)

    def period_events(self, index):
        mi = _month_index(self.start.year, self.start.month) + index * self.interval
        year, month = _split_month_index(mi)
        limit = _days_in_month(year, month)
        out = []
        for day in self.by_monthday:
            if day <= limit:  # day 31 is skipped in short months
                out.append((_dt.date(year, month, day), f"M{day}"))
        return out


@dataclass(frozen=True)
class Window:
    """Half-open instant interval ``[start, end)``."""

    start: Instant
    end: Instant

    def __post_init__(self):
        if self.end.epoch_us < self.start.epoch_us:
            raise ValueError("window end precedes start")

    def contains(self, instant: Instant) -> bool:
        return self.start.epoch_us <= instant.epoch_us < self.end.epoch_us


class Schedule:
    """A single rule plus merged exclusions and one-off additions."""

    def __init__(
        self,
        rule: Rule,
        table: OffsetTable,
        *,
        exdates: set[_dt.date] | None = None,
        rdates: list[WallTime] | None = None,
        gap: str = Gap.FORWARD,
        ambiguous: str = Ambiguous.EARLIEST,
    ):
        self.rule = rule
        self.table = table
        self.exdates = set(exdates or ())
        self.rdates = sorted(rdates or (), key=lambda w: w.epoch_us)
        self.gap_policy = gap
        self.ambiguous_policy = ambiguous

    @property
    def name(self) -> str:
        return self.rule.name

    @property
    def fingerprint(self) -> str:
        parts = self.rule.fingerprint_parts() + [
            sorted(d.isoformat() for d in self.exdates),
            [w.isoformat() for w in self.rdates],
            self.gap_policy,
            self.ambiguous_policy,
        ]
        return _canonical_hash(parts)

    def _resolve(self, wall: WallTime) -> list[tuple[Instant, WallTime, int, str]]:
        result: Resolved = self.table.resolve(
            wall, gap=self.gap_policy, ambiguous=self.ambiguous_policy
        )
        if result.kind == "ambiguous" and result.instants:
            return [
                (inst, wall, wall.epoch_us - inst.epoch_us, result.kind)
                for inst in result.instants
            ]
        assert result.instant is not None
        mapped_wall = result.adjusted_to if result.adjusted_to is not None else wall
        return [(result.instant, mapped_wall, result.offset_us, result.kind)]

    # -- expansion ---------------------------------------------------------

    def expand(self, window: Window) -> Expansion:
        w_start, _ = self.table.wall_at(window.start)
        w_end, _ = self.table.wall_at(window.end)
        lo = w_start.date - _dt.timedelta(days=2)
        hi = w_end.date + _dt.timedelta(days=2)

        candidates, jumps = self.rule.candidate_dates(lo, hi)
        events: list[Event] = []
        seen: set[int] = set()

        def add(instant, nominal, mapped, off, source, kind):
            # Filtering/dedup is on the absolute time line; ``nominal`` keeps
            # the calendar expression even when a gap moves the instant.
            if not window.contains(instant) or instant.epoch_us in seen:
                return
            seen.add(instant.epoch_us)
            shown = mapped if mapped is not None and mapped != nominal else None
            events.append(Event(instant, nominal, off, source, kind, shown))

        for date in candidates:
            if date in self.exdates:
                continue  # exclusion wins over the rule on this nominal date
            nominal = WallTime(
                date.year, date.month, date.day, self.rule.time_of_day_us
            )
            for instant, mapped, off, _res_kind in self._resolve(nominal):
                add(instant, nominal, mapped, off, self.rule.name, "rule")

        # One-off additions; an extra on an excluded date is still dropped.
        for nominal in self.rdates:
            if nominal.date in self.exdates or not (lo <= nominal.date < hi):
                continue
            for instant, mapped, off, _res_kind in self._resolve(nominal):
                add(instant, nominal, mapped, off, f"{self.rule.name}:extra", "extra")

        events.sort(key=lambda e: (e.instant.epoch_us, e.source))
        return Expansion(events, jumps)
