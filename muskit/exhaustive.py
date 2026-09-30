"""Exhaustive power-set verification for small instances.

For small constraint pools every subset is solved and classified; the
classification is ground truth used to verify

* the category of an extracted core (subset-minimal vs minimum-cardinality);
* recovery consistency: re-running extraction on the core returns the core
  itself (fixed point), and the original set is still conflicting.

Subsets the solver cannot decide within budget are recorded as unknown and are
never counted as UNSAT evidence.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from itertools import combinations
from typing import Callable, Iterable, Optional

from .constraints import Problem
from .solver import SAT, UNKNOWN, UNSAT, SolveResult

SolveFn = Callable[[Iterable[str], Optional[int]], SolveResult]


@dataclass
class Classification:
    unsat_sets: list[frozenset] = field(default_factory=list)
    sat_sets: list[frozenset] = field(default_factory=list)
    unknown_sets: list[frozenset] = field(default_factory=list)

    def subset_minimal_unsat(self) -> list[frozenset]:
        return [u for u in self.unsat_sets if not any(v < u for v in self.unsat_sets)]

    def minimum_cardinality(self) -> Optional[int]:
        return min((len(u) for u in self.unsat_sets), default=None)

    def minimum_cardinality_cores(self) -> list[frozenset]:
        m = self.minimum_cardinality()
        return [u for u in self.unsat_sets if len(u) == m] if m is not None else []


def classify_all_subsets(
    problem: Problem,
    solve: SolveFn,
    constraint_ids: Optional[Iterable[str]] = None,
    budget_per_solve: Optional[int] = None,
) -> Classification:
    ids = list(constraint_ids) if constraint_ids is not None else problem.constraint_ids()
    result = Classification()
    for size in range(0, len(ids) + 1):
        for combo in combinations(ids, size):
            r = solve(combo, budget_per_solve)
            entry = frozenset(combo)
            if r.status == UNSAT:
                result.unsat_sets.append(entry)
            elif r.status == SAT:
                result.sat_sets.append(entry)
            else:
                result.unknown_sets.append(entry)
    return result


def verify_subset_minimal_core(core: Iterable[str], classification: Classification) -> tuple[bool, str]:
    core_set = frozenset(core)
    if core_set not in classification.unsat_sets:
        return False, "core is not UNSAT under exhaustive classification"
    for cid in core_set:
        remainder = frozenset(x for x in core_set if x != cid)
        if remainder in classification.unsat_sets:
            return False, f"removing {cid} leaves an UNSAT set: core is not subset-minimal"
        if remainder in classification.unknown_sets:
            return False, f"removing {cid} is undecided: cannot confirm subset-minimality"
    return True, "core is UNSAT and deleting any single item breaks the conflict"


def verify_minimum_cardinality_core(core: Iterable[str], classification: Classification) -> tuple[bool, str]:
    core_set = frozenset(core)
    exact = classification.minimum_cardinality()
    if core_set not in classification.unsat_sets:
        return False, "core is not UNSAT under exhaustive classification"
    if exact is None or len(core_set) != exact:
        return False, f"core size {len(core_set)} != exhaustive minimum {exact}"
    return True, f"core size matches exhaustive minimum {exact}"


def recovery_consistency(
    problem: Problem,
    solve: SolveFn,
    core: Iterable[str],
    original_ids: Iterable[str],
    extract_fn,
    budget_per_check: Optional[int] = None,
) -> tuple[bool, str]:
    """Re-extract from the core (fixed point) and confirm the original conflict."""
    core = list(core)
    again = extract_fn(problem, solve, core, budget_per_check)
    if set(again.core) != set(core):
        return False, f"re-extraction from core yielded {again.core}, expected {core}"
    original = solve(list(original_ids), budget_per_check)
    if original.status != UNSAT:
        return False, f"original set no longer UNSAT (got {original.status})"
    return True, "core is a fixed point of extraction and the original set is still UNSAT"
