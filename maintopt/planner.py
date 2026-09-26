"""Exact branch-and-bound planner (no external solver).

Outcomes are reported as three separate statuses:

* ``OPTIMAL``          -- the search space was exhausted; the returned plan
                          is proven optimal.
* ``FEASIBLE``         -- the node budget ran out; the returned plan is the
                          best feasible plan found, optimality unproven.
* ``BUDGET_UNDECIDED`` -- the node budget ran out before any feasible plan
                          (even the empty one) could be evaluated.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from itertools import combinations

from .model import Instance
from .verify import plan_cost, plan_score, segments_of, validate_instance

OPTIMAL = "OPTIMAL"
FEASIBLE = "FEASIBLE"
BUDGET_UNDECIDED = "BUDGET_UNDECIDED"


class _NodeBudgetExhausted(Exception):
    pass


@dataclass(frozen=True)
class SearchResult:
    status: str                                  # one of the constants above
    plan: dict[str, tuple[int, ...]] | None      # None only for BUDGET_UNDECIDED
    score: Fraction | None
    cost: int | None
    bound: Fraction | None  # valid upper bound on the optimum; None if OPTIMAL
    nodes_explored: int
    node_budget: int


def _placements(instance: Instance, device) -> list[tuple[tuple[int, ...], int]]:
    """All (slots, cost) options for a device, ignoring other devices."""
    free = [t for t in range(instance.horizon) if t not in instance.blocked_slots]
    options = []
    for slots in combinations(free, device.duration):
        cost = device.startup_cost * segments_of(slots)
        if cost <= instance.budget:
            options.append((slots, cost))
    options.sort()
    return options


def solve(instance: Instance, node_budget: int = 1_000_000) -> SearchResult:
    """Maximise total score weight subject to all constraints."""
    validate_instance(instance)
    if node_budget < 0:
        raise ValueError("node_budget must be >= 0")

    devices = sorted(instance.devices, key=lambda d: (-d.score, d.name))
    count = len(devices)
    suffix_score = [Fraction(0)] * (count + 1)
    for i in range(count - 1, -1, -1):
        suffix_score[i] = suffix_score[i + 1] + devices[i].score

    options = {device.name: _placements(instance, device) for device in devices}
    occupancy = {group: [0] * instance.horizon for group in instance.group_limits}

    incumbent: dict = {"plan": None, "score": None}
    nodes = 0

    def search(index: int, remaining_budget: int, chosen: dict, score: Fraction) -> None:
        nonlocal nodes
        nodes += 1
        if nodes > node_budget:
            raise _NodeBudgetExhausted
        # `chosen` is always feasible here, so it is a valid incumbent.
        if incumbent["score"] is None or score > incumbent["score"]:
            incumbent["plan"] = dict(chosen)
            incumbent["score"] = score
        if index == count:
            return
        if score + suffix_score[index] <= incumbent["score"]:
            return  # relaxation bound cannot beat the incumbent
        device = devices[index]
        used = occupancy[device.group]
        limit = instance.group_limits[device.group]
        for slots, cost in options[device.name]:
            if cost > remaining_budget:
                continue
            if any(used[t] + 1 > limit for t in slots):
                continue
            for t in slots:
                used[t] += 1
            chosen[device.name] = slots
            search(index + 1, remaining_budget - cost, chosen, score + device.score)
            del chosen[device.name]
            for t in slots:
                used[t] -= 1
        search(index + 1, remaining_budget, chosen, score)  # skip this device

    exhausted = False
    try:
        search(0, instance.budget, {}, Fraction(0))
    except _NodeBudgetExhausted:
        exhausted = True

    if exhausted:
        status = FEASIBLE if incumbent["plan"] is not None else BUDGET_UNDECIDED
    else:
        status = OPTIMAL

    plan = incumbent["plan"]
    score = incumbent["score"]
    cost = plan_cost(instance, plan) if plan is not None else None
    # For unfinished searches report a valid (loose) bound: every device taken.
    bound = None if status == OPTIMAL else suffix_score[0]
    return SearchResult(
        status=status,
        plan=plan,
        score=score,
        cost=cost,
        bound=bound,
        nodes_explored=nodes,
        node_budget=node_budget,
    )
