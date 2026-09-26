"""Brute-force enumeration of Pareto-optimal plan combinations.

No external solver is used: every combination (one plan per task) is
enumerated explicitly and filtered with exact rational arithmetic.

Categories (kept strictly separate):

* ``OPTIMAL``           hard-feasible, within budget, Pareto non-dominated
* ``FEASIBLE``          hard-feasible, within budget, but dominated
* ``BUDGET_UNDECIDED``  hard-feasible, over budget, Pareto non-dominated

Over-budget combinations that are dominated are dropped entirely.

Because every over-budget combination has a strictly higher cost than every
within-budget one, an over-budget combination can never dominate a
within-budget one; the joint Pareto front over all hard-feasible
combinations therefore splits cleanly into the categories above.
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from itertools import product
from typing import Dict, List, Sequence, Tuple

from .models import Combination, Constraints, Plan


class Category(str, Enum):
    OPTIMAL = "optimal"
    FEASIBLE = "feasible"
    BUDGET_UNDECIDED = "budget_undecided"


def dominates(a: Sequence, b: Sequence) -> bool:
    """True iff objective vector ``a`` Pareto-dominates ``b`` (minimisation).

    Vectors that are better in one dimension and worse in another are
    incomparable: neither dominates the other, so both are kept.
    """
    return all(x <= y for x, y in zip(a, b)) and any(x < y for x, y in zip(a, b))


def pareto_front_indices(objectives: Sequence[Tuple]) -> List[int]:
    """Indices of the non-dominated objective vectors (O(n^2), exact)."""
    front = []
    for i, vec in enumerate(objectives):
        if any(j != i and dominates(other, vec) for j, other in enumerate(objectives)):
            continue
        front.append(i)
    return front


def keep_equal_value_representatives(
    combos: Sequence[Combination],
) -> List[Combination]:
    """For identical objective vectors keep only the smallest combo id."""
    best: Dict[Tuple, Combination] = {}
    for combo in combos:
        current = best.get(combo.objectives)
        if current is None or combo.combo_id < current.combo_id:
            best[combo.objectives] = combo
    return list(best.values())


@dataclass
class CategoryResult:
    """One result category with explicit truncation metadata."""

    category: Category
    combinations: List[Combination]
    total_count: int  # before truncation
    truncated: bool
    omitted_count: int


@dataclass
class EnumerationResult:
    optimal: CategoryResult
    feasible: CategoryResult
    budget_undecided: CategoryResult
    total_combinations: int  # every combination considered
    hard_feasible_count: int  # combinations passing the hard limits

    def categories(self) -> Tuple[CategoryResult, ...]:
        return (self.optimal, self.feasible, self.budget_undecided)


def _finalise(
    category: Category, combos: List[Combination], max_results: int
) -> CategoryResult:
    combos = keep_equal_value_representatives(combos)
    combos.sort(key=lambda c: c.sort_key())
    total = len(combos)
    kept = combos[:max_results]
    return CategoryResult(
        category=category,
        combinations=kept,
        total_count=total,
        truncated=total > max_results,
        omitted_count=total - len(kept),
    )


def enumerate_combinations(
    plans: Sequence[Plan],
    constraints: Constraints = None,
    max_results: int = 100,
) -> EnumerationResult:
    """Enumerate all plan combinations and split them into categories.

    Every returned combination is independently feasible w.r.t. the hard
    limits (``max_latency`` / ``max_energy``). Each category keeps at most
    ``max_results`` entries; truncation is reported explicitly via
    ``truncated`` / ``omitted_count``.
    """
    if constraints is None:
        constraints = Constraints()
    if isinstance(max_results, bool) or not isinstance(max_results, int):
        raise TypeError("max_results must be an int")
    if max_results < 0:
        raise ValueError("max_results must be >= 0")

    plans = list(plans)
    if not plans:
        raise ValueError("at least one plan is required")

    tasks: Dict[str, List[Plan]] = {}
    for plan in plans:
        bucket = tasks.setdefault(plan.task_id, [])
        if any(p.plan_id == plan.plan_id for p in bucket):
            raise ValueError(
                f"duplicate plan id {plan.plan_id!r} in task {plan.task_id!r}"
            )
        bucket.append(plan)

    task_ids = sorted(tasks)
    for task_id in task_ids:
        tasks[task_id].sort(key=lambda p: p.plan_id)

    hard_feasible: List[Combination] = []
    total = 0
    for chosen in product(*(tasks[task_id] for task_id in task_ids)):
        total += 1
        combo = Combination.from_plans(chosen)
        if constraints.max_latency is not None and combo.latency > constraints.max_latency:
            continue
        if constraints.max_energy is not None and combo.energy > constraints.max_energy:
            continue
        hard_feasible.append(combo)

    def within_budget(combo: Combination) -> bool:
        return constraints.max_cost is None or combo.cost <= constraints.max_cost

    front_idx = set(pareto_front_indices([c.objectives for c in hard_feasible]))
    optimal: List[Combination] = []
    feasible: List[Combination] = []
    undecided: List[Combination] = []
    for i, combo in enumerate(hard_feasible):
        on_front = i in front_idx
        if within_budget(combo):
            (optimal if on_front else feasible).append(combo)
        elif on_front:
            undecided.append(combo)
        # over budget and dominated -> dropped, not returned anywhere

    return EnumerationResult(
        optimal=_finalise(Category.OPTIMAL, optimal, max_results),
        feasible=_finalise(Category.FEASIBLE, feasible, max_results),
        budget_undecided=_finalise(Category.BUDGET_UNDECIDED, undecided, max_results),
        total_combinations=total,
        hard_feasible_count=len(hard_feasible),
    )

