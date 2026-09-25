"""Day-by-day reference expansion used only as a test oracle.

Production code never scans days this way; tests compare its result against
the jumping expander to cross-check correctness on small windows.
"""

from __future__ import annotations

import datetime as dt

from recur import WallTime, Window
from recur import rules as _rules
from recur.rules import _days_in_month


def _rule_matches(rule, date: dt.date) -> bool:
    if date < rule.start.date:
        return False
    if rule.frequency == "daily":
        return (date.toordinal() - rule.start.date.toordinal()) % rule.interval == 0
    if rule.frequency == "weekly":
        period_days = (
            date - (rule.start.date - dt.timedelta(days=rule.start.date.weekday()))
        ).days
        if period_days < 0 or period_days % (7 * rule.interval) >= 7:
            return False
        return date.weekday() in rule.by_weekday
    if rule.frequency == "monthly":
        idx = date.year * 12 + (date.month - 1)
        p0 = rule.start.year * 12 + (rule.start.month - 1)
        if idx < p0 or (idx - p0) % rule.interval:
            return False
        return date.day in rule.by_monthday and date.day <= _days_in_month(
            date.year, date.month
        )
    raise AssertionError(rule.frequency)


def brute_force_days(schedule, window: Window):
    """Walk every wall day in range; count scanned days for audit."""
    lo_wall, _ = schedule.table.wall_at(window.start)
    hi_wall, _ = schedule.table.wall_at(window.end)
    date = lo_wall.date - dt.timedelta(days=2)
    end = hi_wall.date + dt.timedelta(days=2)
    scanned = 0
    instants = set()
    events = {}
    while date < end:
        scanned += 1
        if date not in schedule.exdates and _rule_matches(schedule.rule, date):
            wall = WallTime(date.year, date.month, date.day, schedule.rule.time_of_day_us)
            _collect(schedule, wall, window, instants, events, "rule", schedule.rule.name)
        date += dt.timedelta(days=1)
    for wall in schedule.rdates:
        if wall.date in schedule.exdates:
            continue
        _collect(schedule, wall, window, instants, events, "extra", schedule.rule.name + ":extra")
    ordered = [events[k] for k in sorted(events)]
    return ordered, scanned


def _collect(schedule, wall, window, instants, events, kind, source):
    result = schedule.table.resolve(
        wall, gap=schedule.gap_policy, ambiguous=schedule.ambiguous_policy
    )
    if result.kind == "ambiguous" and result.instants:
        pairs = [(inst, wall, wall, wall.epoch_us - inst.epoch_us) for inst in result.instants]
    else:
        assert result.instant is not None
        mapped = result.adjusted_to if result.adjusted_to is not None else wall
        pairs = [(result.instant, wall, mapped, result.offset_us)]
    for instant, nominal, mapped, off in pairs:
        if window.contains(instant) and instant.epoch_us not in instants:
            instants.add(instant.epoch_us)
            shown = mapped if mapped != nominal else None
            events[instant.epoch_us] = _rules.Event(
                instant, nominal, off, source, kind, shown
            )
