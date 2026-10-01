"""Exact branch-and-bound repair search (no external solver).

Objectives are lexicographic:

1. minimise the number of *moved* tasks (original, non-executed tasks whose
   placement differs from the original plan) -- unaffected tasks therefore
   prefer to stay exactly where they were;
2. minimise the total assignment cost of all non-frozen tasks.

The search is complete for rational input data: all time constants are
rational, so an optimal placement exists on the 1/D grid where D is the
common denominator of every input constant (difference constraints with
rational bounds have a solution on that grid whenever they have one).
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from fractions import Fraction
from typing import Dict, List, Optional, Tuple

MAX_GRID_DENOMINATOR = 1_000_000
MAX_SLOTS_PER_TASK = 100_000


@dataclass(frozen=True)
class Slot:
    """One candidate placement for a task."""

    rid: str
    start: Fraction
    cost: Fraction
    is_original: bool


@dataclass
class _ResourceState:
    capacity: int
    intervals: List[Tuple[Fraction, Fraction]]  # occupied [start, end)


def _max_concurrency(intervals: List[Tuple[Fraction, Fraction]],
                     start: Fraction, end: Fraction) -> int:
    """Max number of ``intervals`` simultaneously overlapping ``[start, end)``."""
    points = {start, end}
    clipped = []
    for s, e in intervals:
        cs, ce = max(s, start), min(e, end)
        if cs < ce:
            clipped.append((cs, ce))
            points.add(cs)
            points.add(ce)
    ordered = sorted(points)
    best = 0
    for a, b in zip(ordered, ordered[1:]):
        count = sum(1 for s, e in clipped if s <= a and e >= b)
        if count > best:
            best = count
    return best


def _fits(state: _ResourceState, start: Fraction, end: Fraction) -> bool:
    return _max_concurrency(state.intervals, start, end) < state.capacity


def common_grid_denominator(values) -> int:
    denom = 1
    for v in values:
        denom = math.lcm(denom, v.denominator)
        if denom > MAX_GRID_DENOMINATOR:
            raise ValueError(
                f"input rationals need grid denominator > {MAX_GRID_DENOMINATOR}; "
                "refuse to build an exploding search grid"
            )
    return denom


def build_slots(task, original, resources, now: Fraction, grid: int) -> List[Slot]:
    """All feasible (resource, start) candidates of ``task`` on the grid.

    Feasible means: inside one availability window, not before ``now`` or the
    release time, and ending by the deadline.  Occupancy by other tasks is
    checked during the search, not here.
    """
    slots: Dict[Tuple[str, Fraction], Slot] = {}
    for rid, cost in task.costs.items():
        resource = resources.get(rid)
        if resource is None:
            continue
        for window in resource.windows:
            lo = max(now, task.release, window.start)
            hard_end = window.end if task.deadline is None else min(window.end, task.deadline)
            hi = hard_end - task.duration
            if lo > hi:
                continue
            k0 = math.ceil(lo * grid)
            k1 = math.floor(hi * grid)
            if k1 - k0 + 1 > MAX_SLOTS_PER_TASK:
                raise ValueError(
                    f"task {task.tid!r} has more than {MAX_SLOTS_PER_TASK} candidate "
                    "slots on the rational grid; use coarser input data"
                )
            for k in range(k0, k1 + 1):
                start = Fraction(k, grid)
                key = (rid, start)
                if key not in slots:
                    is_original = (
                        original is not None
                        and original.rid == rid
                        and original.start == start
                    )
                    slots[key] = Slot(rid, start, cost, is_original)
    ordered = sorted(slots.values(), key=lambda s: (not s.is_original, s.cost, s.start, s.rid))
    return ordered


@dataclass
class SearchOutcome:
    incumbent: Optional[Dict[str, Slot]]  # tid -> chosen slot
    incumbent_key: Optional[Tuple[int, Fraction]]  # (moved, cost)
    nodes: int
    aborted: bool  # True iff the node budget stopped the search


def search(order, slots_by_tid, original_tids, durations, resources, frozen_intervals,
           forced_suffix, node_budget: int) -> SearchOutcome:
    """Depth-first branch and bound over ``order`` (list of task ids)."""
    states = {
        rid: _ResourceState(res.capacity, list(frozen_intervals.get(rid, [])))
        for rid, res in resources.items()
    }
    min_cost = {tid: min(s.cost for s in slots_by_tid[tid]) for tid in order}
    cost_suffix = [Fraction(0)] * (len(order) + 1)
    for i in range(len(order) - 1, -1, -1):
        cost_suffix[i] = cost_suffix[i + 1] + min_cost[order[i]]

    assign: Dict[str, Slot] = {}
    incumbent: Optional[Dict[str, Slot]] = None
    incumbent_key: Optional[Tuple[int, Fraction]] = None
    nodes = 0
    aborted = False

    def dfs(i: int, moves: int, cost: Fraction) -> None:
        nonlocal nodes, aborted, incumbent, incumbent_key
        if aborted:
            return
        nodes += 1
        if nodes > node_budget:
            aborted = True
            return
        if incumbent_key is not None:
            min_moves = moves + forced_suffix[i]
            if min_moves > incumbent_key[0]:
                return
            if min_moves == incumbent_key[0] and cost + cost_suffix[i] >= incumbent_key[1]:
                return
        if i == len(order):
            key = (moves, cost)
            if incumbent_key is None or key < incumbent_key:
                incumbent_key = key
                incumbent = dict(assign)
            return
        tid = order[i]
        duration = durations[tid]
        counts_as_move = 1 if tid in original_tids else 0
        for slot in slots_by_tid[tid]:
            state = states[slot.rid]
            end = slot.start + duration
            if not _fits(state, slot.start, end):
                continue
            state.intervals.append((slot.start, end))
            assign[tid] = slot
            move_delta = 0 if slot.is_original else counts_as_move
            dfs(i + 1, moves + move_delta, cost + slot.cost)
            del assign[tid]
            state.intervals.pop()
            if aborted:
                return

    dfs(0, 0, Fraction(0))
    return SearchOutcome(incumbent, incumbent_key, nodes, aborted)
