"""Minimal evidence subset search.

For small evidence sets the minimum subset is verified by exhaustive
enumeration in increasing size.  When the enumeration budget is
insufficient, a feasible (irreducible) witness and the unproven optimum
are reported separately instead of claiming optimality.
"""
from __future__ import annotations

import itertools
from dataclasses import dataclass
from typing import Optional, Sequence, Tuple

from .engine import satisfied_at, time_points
from .model import Evidence, Rule

Interval = Tuple[int, int]

DEFAULT_BUDGET = 200_000


def covers_target(
    rule: Rule, subset: Sequence[Evidence], target: Interval
) -> bool:
    """True iff the rule holds over the whole target using only subset."""
    points = time_points(subset, target)
    for a, b in zip(points, points[1:]):
        if a >= b or b <= target[0] or a >= target[1]:
            continue
        if not satisfied_at(rule, subset, a):
            return False
    return True


def _ids(items) -> Tuple[str, ...]:
    return tuple(item.evidence_id for item in items)


def _greedy_witness(
    rule: Rule, items: Sequence[Evidence], target: Interval
) -> Optional[Tuple[Evidence, ...]]:
    """An irreducible feasible subset (minimal, not necessarily minimum)."""
    if not covers_target(rule, items, target):
        return None
    kept = list(items)
    changed = True
    while changed:
        changed = False
        for item in list(kept):
            trial = [x for x in kept if x is not item]
            if covers_target(rule, trial, target):
                kept = trial
                changed = True
    return tuple(kept)


@dataclass(frozen=True)
class SubsetResult:
    target: Interval
    budget: int
    enumerated: int
    feasible: bool
    proven_optimal: bool
    optimal: Optional[Tuple[str, ...]]
    optimal_size: Optional[int]
    witness: Optional[Tuple[str, ...]]

    def as_dict(self):
        return {
            "target": list(self.target),
            "budget": self.budget,
            "enumerated": self.enumerated,
            "feasible": self.feasible,
            "proven_optimal": self.proven_optimal,
            "optimal": list(self.optimal) if self.optimal else None,
            "optimal_size": self.optimal_size,
            "witness": list(self.witness) if self.witness else None,
        }


def minimal_subset(
    rule: Rule,
    evidence: Sequence[Evidence],
    target: Interval,
    budget: int = DEFAULT_BUDGET,
) -> SubsetResult:
    """Minimum-cardinality evidence subset covering the target interval.

    Enumerates subsets in increasing size; the first feasible one is
    provably minimum.  If the budget runs out first, returns a greedy
    feasible witness with ``proven_optimal=False``.
    """
    items = list(evidence)
    enumerated = 0
    for size in range(0, len(items) + 1):
        for combo in itertools.combinations(items, size):
            if enumerated >= budget:
                witness = _greedy_witness(rule, items, target)
                return SubsetResult(
                    target=target,
                    budget=budget,
                    enumerated=enumerated,
                    feasible=witness is not None,
                    proven_optimal=False,
                    optimal=None,
                    optimal_size=None,
                    witness=_ids(witness) if witness else None,
                )
            enumerated += 1
            if covers_target(rule, list(combo), target):
                return SubsetResult(
                    target=target,
                    budget=budget,
                    enumerated=enumerated,
                    feasible=True,
                    proven_optimal=True,
                    optimal=_ids(combo),
                    optimal_size=size,
                    witness=_ids(combo),
                )
    return SubsetResult(
        target=target,
        budget=budget,
        enumerated=enumerated,
        feasible=False,
        proven_optimal=False,
        optimal=None,
        optimal_size=None,
        witness=None,
    )
