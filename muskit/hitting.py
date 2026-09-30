"""Minimum-cardinality core search via hitting sets (MARCO-style).

A subset-minimal core (deletion-based) is not necessarily a
minimum-cardinality core.  To find the smallest conflicting subset we explore
the power-set lattice with two kinds of blocking evidence:

* every UNSAT set found blocks all of its supersets (monotone unsatisfiability);
* every SAT set found blocks all of its subsets: any conflicting set must
  "hit" its complement (a correction set).

The size of any family of pairwise-disjoint correction sets is a *lower bound*
on the minimum core size, because a conflicting set must contain at least one
element of each of them.  When the best core found meets the lower bound (or
the lattice is exhausted with no undecided seed) the optimum is *proven*;
otherwise -- and whenever the internal solver reports UNKNOWN or a budget runs
out -- the result is UNDECIDED rather than a guessed answer.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from itertools import combinations
from typing import Callable, Iterable, Optional

from .checker import check_assignment
from .constraints import Problem
from .solver import SAT, UNKNOWN, UNSAT, SolveResult

SolveFn = Callable[[Iterable[str], Optional[int]], SolveResult]

OPTIMAL = "OPTIMAL"
UNDECIDED = "UNDECIDED"
NOT_CONFLICTING = "NOT_CONFLICTING"


@dataclass
class MinCardResult:
    best_core: Optional[list[str]]
    status: str                              # OPTIMAL | UNDECIDED | NOT_CONFLICTING
    proven: bool
    lower_bound: int                         # from disjoint correction-set packing
    upper_bound: Optional[int]               # size of the best core found
    cores: list[list[str]] = field(default_factory=list)
    correction_sets: list[list[str]] = field(default_factory=list)
    log: list[str] = field(default_factory=list)


def _shrink(problem: Problem, solve: SolveFn, seed, budget_per_check):
    """Deletion-based shrink of an UNSAT seed. Returns (unsat_subset, decisive)."""
    core = list(seed)
    decisive = True
    for cid in list(core):
        trial = [x for x in core if x != cid]
        result = solve(trial, budget_per_check)
        if result.status == UNSAT:
            core = trial
        elif result.status == UNKNOWN:
            decisive = False  # keep the item; core stays UNSAT but maybe not minimal
        else:
            bad = check_assignment(problem, trial, result.assignment or {})
            if bad:
                raise AssertionError(f"solver claimed SAT but checker rejects: {bad}")
    return core, decisive


def _grow(problem: Problem, solve: SolveFn, seed, universe, budget_per_check):
    """Grow a SAT seed towards a maximal SAT set (best effort under budget)."""
    sat_set = list(seed)
    for cid in universe:
        if cid in sat_set:
            continue
        trial = sat_set + [cid]
        result = solve(trial, budget_per_check)
        if result.status == SAT:
            bad = check_assignment(problem, trial, result.assignment or {})
            if bad:
                raise AssertionError(f"solver claimed SAT but checker rejects: {bad}")
            sat_set = trial
        # UNSAT -> cannot add; UNKNOWN -> budget out.  Either way the current
        # set stays SAT, and the complement of *any* SAT set is a valid
        # correction set, so blocking remains sound.
    return sat_set


def _max_disjoint_packing(sets) -> int:
    """Exact maximum family of pairwise-disjoint sets (exponential; small inputs)."""
    pools = [set(s) for s in sets]
    best = [0]

    def rec(idx: int, used: frozenset, count: int) -> None:
        if count + (len(pools) - idx) <= best[0]:
            return
        if idx == len(pools):
            best[0] = max(best[0], count)
            return
        if not (pools[idx] & used):
            rec(idx + 1, used | pools[idx], count + 1)
        rec(idx + 1, used, count)

    rec(0, frozenset(), 0)
    return best[0]


def find_min_cardinality_core(
    problem: Problem,
    solve: SolveFn,
    constraint_ids: Optional[Iterable[str]] = None,
    budget_per_check: Optional[int] = None,
    candidate_budget: int = 10000,
) -> MinCardResult:
    universe = sorted(constraint_ids) if constraint_ids is not None else sorted(problem.constraint_ids())
    log: list[str] = []
    cores: list[list[str]] = []
    corrections: list[list[str]] = []
    skipped: list[frozenset] = []   # seeds the solver could not decide (UNKNOWN)
    undecided = False
    exhausted = False
    candidates_seen = 0

    def next_seed():
        nonlocal candidates_seen
        core_sets = [set(c) for c in cores]
        corr_sets = [set(c) for c in corrections]
        for size in range(0, len(universe) + 1):
            for combo in combinations(universe, size):
                s = set(combo)
                if any(s >= c for c in core_sets):
                    continue  # superset of a known UNSAT set
                if any(not (s & m) for m in corr_sets):
                    continue  # misses a correction set -> cannot be UNSAT
                if frozenset(combo) in skipped:
                    continue  # solver was undecided here before
                if candidates_seen >= candidate_budget:
                    return "BUDGET"
                candidates_seen += 1
                return sorted(combo)
        return None

    def lower_bound() -> int:
        return _max_disjoint_packing(corrections) if corrections else 0

    while True:
        best = min(cores, key=len, default=None)
        if best is not None and lower_bound() == len(best):
            log.append(f"lower bound {lower_bound()} meets best core size {len(best)}: proven optimal")
            break
        seed = next_seed()
        if seed is None:
            exhausted = True
            log.append("lattice exhausted: no unexplored seed remains")
            break
        if seed == "BUDGET":
            log.append(f"candidate budget {candidate_budget} exhausted")
            break
        result = solve(seed, budget_per_check)
        if result.status == UNSAT:
            core, decisive = _shrink(problem, solve, seed, budget_per_check)
            cores.append(core)
            log.append(
                f"UNSAT seed {seed} -> core {core} (size {len(core)}"
                f"{'' if decisive else ', shrink undecided'})"
            )
            if not decisive:
                undecided = True
        elif result.status == SAT:
            bad = check_assignment(problem, seed, result.assignment or {})
            if bad:
                raise AssertionError(f"solver claimed SAT but checker rejects: {bad}")
            sat_set = _grow(problem, solve, seed, universe, budget_per_check)
            correction = sorted(set(universe) - set(sat_set))
            corrections.append(correction)
            log.append(f"SAT seed {seed} -> grew to {sorted(sat_set)}, correction set {correction}")
        else:
            # Budget exhausted on this seed: not SAT, not UNSAT -- undecided.
            undecided = True
            skipped.append(frozenset(seed))
            log.append(f"UNKNOWN seed {seed} (solver budget exhausted); skipped, not blocked")

    best = min(cores, key=len, default=None)
    lb = lower_bound()
    ub = len(best) if best is not None else None
    if best is None:
        status = UNDECIDED if undecided or not exhausted else NOT_CONFLICTING
        proven = False
    else:
        proven = lb == ub or (exhausted and not undecided)
        status = OPTIMAL if proven else UNDECIDED
    return MinCardResult(
        best_core=best,
        status=status,
        proven=proven,
        lower_bound=lb,
        upper_bound=ub,
        cores=cores,
        correction_sets=corrections,
        log=log,
    )
