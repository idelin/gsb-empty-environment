"""Endpoint sweep: satisfied intervals, gaps, and the longest interval.

All evidence endpoints (clipped to the horizon) are event points. Between two
consecutive event points the active set is constant, so the joint constraint
is evaluated once per elementary interval. Adjacent satisfied pieces merge:
intervals touching exactly at a boundary leave no zero-length gap.
"""

from __future__ import annotations

from dataclasses import dataclass

from .matching import active_items, is_satisfied
from .model import EvidenceItem, Rule


@dataclass(frozen=True)
class Interval:
    start: int
    end: int

    @property
    def length(self) -> int:
        return self.end - self.start


@dataclass(frozen=True)
class Coverage:
    satisfied: tuple[Interval, ...]
    gaps: tuple[Interval, ...]
    longest: Interval | None


def _merge(intervals: list[Interval]) -> list[Interval]:
    """Union of intervals; touching boundaries ([a,b) + [b,c)) merge."""
    merged: list[Interval] = []
    for iv in sorted(intervals, key=lambda i: (i.start, i.end)):
        if merged and iv.start <= merged[-1].end:
            merged[-1] = Interval(merged[-1].start, max(merged[-1].end, iv.end))
        else:
            merged.append(iv)
    return merged


def compute_coverage(rule: Rule, items: list[EvidenceItem]) -> Coverage:
    bounds = {rule.horizon_start, rule.horizon_end}
    for it in items:
        if it.retracted:
            continue
        lo = max(it.valid_from, rule.horizon_start)
        hi = min(it.valid_until, rule.horizon_end)
        if lo < hi:
            bounds.add(lo)
            bounds.add(hi)
    points = sorted(bounds)

    satisfied_pieces: list[Interval] = []
    for a, b in zip(points, points[1:]):
        if is_satisfied(rule, active_items(items, a, b)):
            satisfied_pieces.append(Interval(a, b))
    satisfied = tuple(_merge(satisfied_pieces))

    gaps: list[Interval] = []
    cursor = rule.horizon_start
    for iv in satisfied:
        if iv.start > cursor:
            gaps.append(Interval(cursor, iv.start))
        cursor = max(cursor, iv.end)
    if cursor < rule.horizon_end:
        gaps.append(Interval(cursor, rule.horizon_end))

    longest = max(satisfied, key=lambda i: i.length, default=None)
    return Coverage(satisfied=satisfied, gaps=tuple(gaps), longest=longest)


def covers_horizon(rule: Rule, items: list[EvidenceItem]) -> bool:
    """True iff the satisfied union covers the whole required horizon."""
    cov = compute_coverage(rule, items)
    return (
        len(cov.satisfied) == 1
        and cov.satisfied[0].start <= rule.horizon_start
        and cov.satisfied[0].end >= rule.horizon_end
    )
