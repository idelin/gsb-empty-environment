"""Minimal evidence subset search with an evaluation budget.

Small sets: exhaustive enumeration by increasing size proves minimality.
When 2**n exceeds the budget, a greedy shrink produces a feasible witness
and optimality is reported as unproven -- the two are never conflated.
"""

from __future__ import annotations

from dataclasses import dataclass
from itertools import combinations

from .model import EvidenceItem, Rule
from .sweep import covers_horizon


@dataclass(frozen=True)
class SubsetResult:
    witness: tuple[str, ...] | None  # item IDs of a covering subset
    witness_size: int | None
    optimality_proven: bool
    feasible: bool
    evaluations: int
    budget: int


def minimal_subset(
    rule: Rule, items: list[EvidenceItem], budget: int = 4096
) -> SubsetResult:
    candidates = [it for it in items if not it.retracted]
    n = len(candidates)

    if not covers_horizon(rule, candidates):
        return SubsetResult(
            witness=None,
            witness_size=None,
            optimality_proven=True,
            feasible=False,
            evaluations=1,
            budget=budget,
        )

    if 2**n <= budget:
        evaluations = 1  # the full-set check above
        for size in range(1, n + 1):
            for combo in combinations(candidates, size):
                evaluations += 1
                if covers_horizon(rule, list(combo)):
                    return SubsetResult(
                        witness=tuple(sorted(it.id for it in combo)),
                        witness_size=size,
                        optimality_proven=True,
                        feasible=True,
                        evaluations=evaluations,
                        budget=budget,
                    )
        raise AssertionError("unreachable: full set covers the horizon")

    # Budget insufficient for a proof: greedy shrink from the full set.
    evaluations = 1
    witness = list(candidates)
    improved = True
    while improved:
        improved = False
        for it in list(witness):
            trial = [x for x in witness if x is not it]
            evaluations += 1
            if covers_horizon(rule, trial):
                witness = trial
                improved = True
    return SubsetResult(
        witness=tuple(sorted(it.id for it in witness)),
        witness_size=len(witness),
        optimality_proven=False,
        feasible=True,
        evaluations=evaluations,
        budget=budget,
    )
