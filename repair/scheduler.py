"""Exact, solver-free single-machine placement optimiser.

Given the free intervals (gaps) of one machine and a set of tasks to
place, find start times minimising the sum of absolute deviations from
each task's ideal start.  The search enumerates task orders and, for a
fixed order, runs an exact branch-and-bound over candidate start times
(gap starts, gap ends minus duration, and ideal starts).  A node budget
caps the work; when the budget is hit the result is flagged incomplete
so the caller can report FEASIBLE / BUDGET_UNDECIDED instead of OPTIMAL.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from itertools import permutations
from typing import Dict, List, Optional, Sequence, Tuple


class BudgetExhausted(Exception):
    """Raised internally when the node budget is exceeded."""


@dataclass(frozen=True)
class Gap:
    """A free interval [start, end); end=None means unbounded."""
    start: Fraction
    end: Optional[Fraction]


@dataclass(frozen=True)
class PlacementItem:
    key: str
    duration: Fraction
    ideal: Fraction  # start the task would prefer (cost = |start-ideal|)


@dataclass
class PlacementResult:
    feasible: bool
    complete: bool                 # False if the budget stopped the search
    cost: Optional[Fraction]
    starts: Dict[str, Fraction] = field(default_factory=dict)
    nodes: int = 0


def compute_gaps(obstacles, now):
    """Subtract busy intervals from [now, infinity) and return the gaps."""
    intervals = []
    for start, end in obstacles:
        if end is not None and end <= now:
            continue
        intervals.append((start if start > now else now, end))
    intervals.sort(key=lambda iv: iv[0])
    merged = []
    for start, end in intervals:
        if merged:
            last_start, last_end = merged[-1]
            if last_end is None or start <= last_end:
                if last_end is None or end is None:
                    merged[-1][1] = None
                elif end > last_end:
                    merged[-1][1] = end
                continue
        merged.append([start, end])
    gaps = []
    cursor = now
    for start, end in merged:
        if start > cursor:
            gaps.append(Gap(cursor, start))
        if end is None:
            return gaps  # permanent obstacle: nothing free afterwards
        if end > cursor:
            cursor = end
    gaps.append(Gap(cursor, None))  # unbounded tail
    return gaps


def _candidates(item, earliest, gaps):
    """Feasible start times that can be optimal for one task."""
    out = set()
    for gap in gaps:
        s0 = gap.start if gap.start > earliest else earliest
        if gap.end is not None and s0 + item.duration > gap.end:
            continue
        out.add(s0)
        if gap.end is not None:
            late = gap.end - item.duration
            if late >= s0:
                out.add(late)
        ideal = item.ideal
        if ideal >= s0 and (gap.end is None or ideal + item.duration <= gap.end):
            out.add(ideal)
    return sorted(out)


def _best_for_order(items, order, gaps, budget, counter, incumbent):
    """Exact min-cost placement for one fixed task order.

    Returns (cost, starts) or None when nothing strictly better than the
    incumbent exists (or the order is infeasible).  Branch-and-bound with
    memoisation; raises BudgetExhausted when the node budget is exceeded.
    """
    n = len(order)
    memo = {}

    def rec(i, earliest, acc):
        counter[0] += 1
        if counter[0] > budget:
            raise BudgetExhausted
        if i == n:
            return (Fraction(0), ())
        key = (i, earliest)
        hit = memo.get(key)
        if hit is not None:
            return hit
        item = items[order[i]]
        best = None
        for start in _candidates(item, earliest, gaps):
            step = abs(start - item.ideal)
            if incumbent[0] is not None and acc + step >= incumbent[0]:
                continue  # cannot improve the incumbent: safe to prune
            sub = rec(i + 1, start + item.duration, acc + step)
            if sub is None:
                continue
            total = (step + sub[0], (start,) + sub[1])
            if best is None or total[0] < best[0]:
                best = total
        if best is not None:
            memo[key] = best
        return best

    return rec(0, gaps[0].start, Fraction(0))


def optimize_placement(items, gaps, budget):
    """Place all items inside the gaps at minimum total deviation cost."""
    if not items:
        return PlacementResult(True, True, Fraction(0), {}, 0)
    if not gaps:
        return PlacementResult(False, True, None, {}, 0)
    # Quick exact infeasibility checks.
    for item in items:
        if not any(g.end is None or g.end - g.start >= item.duration
                   for g in gaps):
            return PlacementResult(False, True, None, {}, 0)
    if all(g.end is not None for g in gaps):
        capacity = sum(g.end - g.start for g in gaps)
        if sum(item.duration for item in items) > capacity:
            return PlacementResult(False, True, None, {}, 0)

    counter = [0]
    incumbent = [None]
    best_starts = None
    best_order = None
    complete = True
    try:
        for order in permutations(range(len(items))):
            result = _best_for_order(items, order, gaps, budget,
                                     counter, incumbent)
            if result is None:
                continue
            cost, starts = result
            if incumbent[0] is None or cost < incumbent[0]:
                incumbent[0] = cost
                best_starts = starts
                best_order = order
    except BudgetExhausted:
        complete = False

    if incumbent[0] is None:
        return PlacementResult(False, complete, None, {}, counter[0])
    assignment = {
        items[best_order[i]].key: best_starts[i] for i in range(len(items))
    }
    return PlacementResult(True, complete, incumbent[0], assignment,
                           counter[0])
