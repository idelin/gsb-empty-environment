"""Greedy heuristic for the budgeted coverage problem.

The result is always reported with status ``FEASIBLE``: the heuristic never
claims optimality and no approximation guarantee is implied (see
``report.gap_report``).

Move semantics: a move selects one set *plus* its missing dependency
closure, so dependency costs are always accounted for.  Zero-cost moves are
processed before any costly move and never divide by cost; empty sets yield
zero gain and are therefore never chosen on their own.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import FrozenSet, List, Optional, Set, Tuple

from .model import Instance, Status, UnsatisfiableDependency


@dataclass
class GreedyResult:
    status: Status
    selected: FrozenSet[str]
    profit: Fraction
    cost: Fraction
    log: List[str] = field(default_factory=list)


def _feasible_move(instance: Instance, selected: Set[str], name: str,
                   current_cost: Fraction, counts) -> Optional[Tuple[frozenset, Fraction, Fraction]]:
    """Return (added_sets, added_cost, exact_gain) or None if the move is infeasible."""
    try:
        closure = instance.dependency_closure(set(selected) | {name})
    except UnsatisfiableDependency:
        return None
    added = frozenset(closure - selected)
    if not added:
        return None
    if instance.conflicting_pairs(closure):
        return None
    add_cost = instance.total_cost(added)
    if current_cost + add_cost > instance.budget:
        return None
    gain = instance.marginal_gain(counts, added)
    return added, add_cost, gain


def greedy(instance: Instance) -> GreedyResult:
    selected: Set[str] = set()
    log: List[str] = []
    while True:
        counts = instance.coverage_counts(selected)
        current_cost = instance.total_cost(selected)
        zero_moves = []    # (gain, name, added) with added_cost == 0
        ratio_moves = []   # (ratio, gain, name, added, added_cost) with cost > 0
        for name in sorted(instance.sets):
            if name in selected:
                continue
            move = _feasible_move(instance, selected, name, current_cost, counts)
            if move is None:
                continue
            added, add_cost, gain = move
            if gain <= 0:
                # Empty sets and non-improving moves add no profit; skip them.
                continue
            if add_cost == 0:
                zero_moves.append((gain, name, added))
            else:
                ratio_moves.append((gain / add_cost, gain, name, added, add_cost))
        if zero_moves:
            # Zero-cost sets first; no ratio is ever computed for them.
            zero_moves.sort(key=lambda t: (t[0], t[1]), reverse=True)
            gain, name, added = zero_moves[0]
            selected |= added
            log.append(f"zero-cost move via {name!r}: add {sorted(added)}, gain {gain}")
        elif ratio_moves:
            ratio_moves.sort(key=lambda t: (t[0], t[1], t[2]), reverse=True)
            ratio, gain, name, added, add_cost = ratio_moves[0]
            selected |= added
            log.append(
                f"ratio move via {name!r}: add {sorted(added)}, "
                f"gain {gain}, cost {add_cost}, density {ratio}"
            )
        else:
            break
    profit = instance.profit_of(selected)
    cost = instance.total_cost(selected)
    log.append(f"greedy done: profit {profit}, cost {cost}/{instance.budget}")
    return GreedyResult(Status.FEASIBLE, frozenset(selected), profit, cost, log)
