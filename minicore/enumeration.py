"""Independent verification by exhaustive subset enumeration (small instances).

For small constraint pools every subset is solved directly (bypassing the
extraction algorithms and their cache), giving ground truth for: the core
category (subset-minimal vs minimum-cardinality), the per-item deletion
witnesses, and recovery consistency (does deleting the core restore
satisfiability of the remainder?).
"""

from __future__ import annotations

from dataclasses import dataclass, field
from itertools import combinations
from typing import Optional, Sequence

from .solver import Problem, Status, solve


def enumerate_all_subsets(
    problem: Problem, budget_per_subset: int = 50_000
) -> dict:
    """Solve every subset of the constraint pool; map frozenset -> Status."""
    n = len(problem.constraints)
    table: dict = {}
    for size in range(n + 1):
        for combo in combinations(range(n), size):
            table[frozenset(combo)] = solve(
                problem, combo, budget=budget_per_subset
            ).status
    return table


@dataclass
class CoreVerification:
    core: tuple[int, ...]
    core_status: Optional[Status] = None
    deletion_witnesses: dict = field(default_factory=dict)  # item -> Status
    is_subset_minimal: Optional[bool] = None       # None = undecided
    is_minimum_cardinality: Optional[bool] = None  # None = undecided
    min_cardinality: Optional[int] = None
    all_muses: list = field(default_factory=list)
    recovery_consistent: Optional[bool] = None     # complement SAT?
    unknown_subsets: list = field(default_factory=list)


def _proper_subsets(core):
    items = sorted(core)
    for size in range(len(items)):
        for combo in combinations(items, size):
            yield frozenset(combo)


def verify_core(
    problem: Problem,
    core: Sequence[int],
    table: Optional[dict] = None,
    budget_per_subset: int = 50_000,
) -> CoreVerification:
    """Independently classify ``core`` against exhaustive enumeration."""
    if table is None:
        table = enumerate_all_subsets(problem, budget_per_subset)
    universe = frozenset(range(len(problem.constraints)))
    core_set = frozenset(core)
    report = CoreVerification(core=tuple(sorted(core_set)))
    report.unknown_subsets = sorted(
        tuple(sorted(s)) for s, st in table.items() if st is Status.UNKNOWN
    )
    report.core_status = table[core_set]

    # Deletion witnesses: status of core minus each single item.
    witnessed_unknown = False
    for item in sorted(core_set):
        status = table[frozenset(core_set - {item})]
        report.deletion_witnesses[item] = status
        if status is Status.UNKNOWN:
            witnessed_unknown = True

    if report.core_status is Status.UNSAT and not witnessed_unknown:
        report.is_subset_minimal = all(
            st is Status.SAT for st in report.deletion_witnesses.values()
        )
    elif report.core_status is Status.UNKNOWN or witnessed_unknown:
        report.is_subset_minimal = None
    else:
        report.is_subset_minimal = False

    # All MUSes from the table (only where the table is decisive).
    decisive = not report.unknown_subsets
    muses = []
    if decisive:
        for subset, status in table.items():
            if status is not Status.UNSAT:
                continue
            if all(table[proper] is Status.SAT for proper in _proper_subsets(subset)):
                muses.append(tuple(sorted(subset)))
        muses.sort(key=lambda s: (len(s), s))
        report.all_muses = muses
        if muses:
            report.min_cardinality = min(len(m) for m in muses)
            report.is_minimum_cardinality = (
                tuple(sorted(core_set)) in muses
                and len(core_set) == report.min_cardinality
            )

    # Recovery consistency: is the remainder satisfiable once the core goes?
    complement = universe - core_set
    comp_status = table[complement]
    if comp_status is Status.SAT:
        report.recovery_consistent = True
    elif comp_status is Status.UNSAT:
        report.recovery_consistent = False
    else:
        report.recovery_consistent = None
    return report
