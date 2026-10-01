"""Exact reference solver: exhaustive subset enumeration for small instances.

This is the ground-truth checker used to validate the branch-and-bound and
to measure the heuristic gap.  It refuses to run on instances that are too
large, so nobody mistakes a greedy run for a proven optimum.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import FrozenSet

from .model import Instance, Status

DEFAULT_MAX_SETS = 22


@dataclass
class ExactResult:
    status: Status
    selected: FrozenSet[str]
    profit: Fraction
    cost: Fraction
    subsets_checked: int


def brute_force(instance: Instance, max_sets: int = DEFAULT_MAX_SETS) -> ExactResult:
    n = len(instance.sets)
    if n > max_sets:
        raise ValueError(
            f"instance has {n} sets; full enumeration is capped at {max_sets}. "
            "Use the branch-and-bound solver instead."
        )
    names = sorted(instance.sets)
    best_profit = Fraction(-1)
    best_selected: FrozenSet[str] = frozenset()
    checked = 0
    for mask in range(1 << n):
        selected = {names[i] for i in range(n) if mask >> i & 1}
        checked += 1
        ev = instance.evaluate(selected)
        if not ev.feasible:
            continue
        if ev.profit > best_profit:
            best_profit = ev.profit
            best_selected = frozenset(selected)
    return ExactResult(
        Status.OPTIMAL,
        best_selected,
        best_profit,
        instance.total_cost(best_selected),
        checked,
    )
