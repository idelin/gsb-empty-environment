"""Conflict-core extraction with a bounded internal-solver budget.

Two different notions are kept strictly apart:

- Subset-minimal core (deletion-based): every remaining item has a
  *deletion witness* -- removing it makes the set satisfiable. This is an
  irreducible inconsistent subset, not necessarily a smallest one.
- Minimum-cardinality core: a smallest inconsistent subset, found by
  hitting-set search over the complements of confirmed-satisfiable subsets,
  with the running minimum-hitting-set size as a lower-bound certificate.

Budget discipline: the internal solver may answer UNKNOWN (budget
exhausted). UNKNOWN is never treated as UNSAT (no item is removed on
UNKNOWN) and never as SAT (no deletion witness is claimed on UNKNOWN).
If any check is UNKNOWN the result is propagated as undecided.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from itertools import combinations
from typing import Optional, Sequence

from .cache import CachedSolver
from .solver import Problem, Status

KIND_SUBSET_MINIMAL = "subset_minimal"
KIND_MINIMUM_CARDINALITY = "minimum_cardinality"
KIND_UNVERIFIED = "unverified_candidate"
KIND_UNDECIDED = "undecided"
KIND_NO_CONFLICT = "no_conflict"


@dataclass
class CheckRecord:
    """One internal-solver check, kept as an audit trail."""

    step: int
    phase: str
    tested: tuple[int, ...]
    status: Status
    nodes: int
    decision: str
    note: str = ""


@dataclass
class CoreResult:
    kind: str
    core: tuple[int, ...]
    records: list[CheckRecord] = field(default_factory=list)
    lower_bound: int = 0
    undecided: bool = False
    total_nodes: int = 0


class _Budget:
    """Shared node budget across all internal-solver calls of one run."""

    def __init__(self, solver: CachedSolver, per_call: int, total: int) -> None:
        self.solver = solver
        self.per_call = per_call
        self.total = total
        self.spent = 0
        self.exhausted = False

    def run(self, indices: Sequence[int]):
        remaining = self.total - self.spent
        if remaining <= 0:
            self.exhausted = True

            class _R:
                status = Status.UNKNOWN
                witness = None
                nodes = 0

            return _R()
        result = self.solver.solve(indices, budget=min(self.per_call, remaining))
        self.spent += result.nodes
        return result


def deletion_based_core(
    problem: Problem,
    solver: CachedSolver,
    per_call_budget: int = 5_000,
    total_budget: int = 200_000,
) -> CoreResult:
    """Deletion-based subset-minimal core with a full check record.

    For each item, the remainder is solved: UNSAT means the item is
    redundant and is dropped; SAT means the item is necessary (the witness
    has already been validated by the independent checker inside
    ``CachedSolver``); UNKNOWN means no conclusion -- the item is kept and
    the run is flagged undecided.
    """
    budget = _Budget(solver, per_call_budget, total_budget)
    records: list[CheckRecord] = []
    undecided = False
    step = 0

    def record(phase, tested, status, nodes, decision, note=""):
        nonlocal step
        step += 1
        records.append(CheckRecord(step, phase, tuple(sorted(tested)), status,
                                   nodes, decision, note))

    universe = tuple(range(len(problem.constraints)))
    first = budget.run(universe)
    record("initial", universe, first.status, first.nodes, "check_full_set")
    if first.status is Status.SAT:
        return CoreResult(KIND_NO_CONFLICT, (), records,
                          total_nodes=budget.spent)
    if first.status is Status.UNKNOWN:
        return CoreResult(KIND_UNDECIDED, universe, records, undecided=True,
                          total_nodes=budget.spent)

    current = list(universe)
    for item in list(current):
        rest = tuple(i for i in current if i != item)
        result = budget.run(rest)
        if result.status is Status.UNSAT:
            current.remove(item)
            record("deletion", rest, result.status, result.nodes,
                   "removed", f"item {item} redundant; still UNSAT without it")
        elif result.status is Status.SAT:
            record("deletion", rest, result.status, result.nodes,
                   "kept_sat", f"item {item} necessary; deletion witness verified")
        else:
            undecided = True
            record("deletion", rest, result.status, result.nodes,
                   "kept_unknown",
                   f"item {item} kept; budget exhausted, cannot conclude")

    final = budget.run(tuple(current))
    record("final_verify", tuple(current), final.status, final.nodes,
           "verify_core")
    if final.status is Status.UNKNOWN:
        return CoreResult(KIND_UNDECIDED, tuple(current), records,
                          undecided=True, total_nodes=budget.spent)
    if undecided:
        # The core is UNSAT, but some deletion checks were inconclusive, so
        # subset-minimality cannot be claimed.
        return CoreResult(KIND_UNVERIFIED, tuple(current), records,
                          undecided=True, total_nodes=budget.spent)
    return CoreResult(KIND_SUBSET_MINIMAL, tuple(current), records,
                      total_nodes=budget.spent)


def _minimum_hitting_set(blockers, universe):
    """Smallest subset of ``universe`` intersecting every blocker (brute force)."""
    if not blockers:
        return frozenset()
    ordered = sorted(universe)
    for size in range(len(ordered) + 1):
        for combo in combinations(ordered, size):
            candidate = frozenset(combo)
            if all(candidate & blocker for blocker in blockers):
                return candidate
    return None


def minimum_cardinality_core(
    problem: Problem,
    solver: CachedSolver,
    per_call_budget: int = 5_000,
    total_budget: int = 200_000,
) -> CoreResult:
    """Minimum-cardinality core via hitting-set (HS) search.

    Invariant: every UNSAT subset must hit the complement of every
    confirmed-SAT subset. Hence the minimum hitting set of the collected
    complements is both the next candidate and a *lower-bound certificate*:
    no core smaller than the current hitting set can exist. A SAT answer
    adds a new blocker; UNSAT closes the gap (lower bound == core size);
    UNKNOWN propagates as undecided with the lower bound still valid.
    """
    budget = _Budget(solver, per_call_budget, total_budget)
    records: list[CheckRecord] = []
    universe = frozenset(range(len(problem.constraints)))
    blockers: list[frozenset] = []
    step = 0

    def record(phase, tested, status, nodes, decision, note=""):
        nonlocal step
        step += 1
        records.append(CheckRecord(step, phase, tuple(sorted(tested)), status,
                                   nodes, decision, note))

    for _ in range(1 << len(universe)):
        candidate = _minimum_hitting_set(blockers, universe)
        if candidate is None:
            # An empty blocker means the full set itself is satisfiable.
            return CoreResult(KIND_NO_CONFLICT, (), records,
                              total_nodes=budget.spent)
        lower_bound = len(candidate)
        result = budget.run(tuple(sorted(candidate)))
        if result.status is Status.UNSAT:
            record("hitting_set", tuple(sorted(candidate)), result.status,
                   result.nodes, "core_found",
                   f"lower bound {lower_bound} met; minimum-cardinality core")
            return CoreResult(KIND_MINIMUM_CARDINALITY,
                              tuple(sorted(candidate)), records,
                              lower_bound=lower_bound, total_nodes=budget.spent)
        if result.status is Status.SAT:
            blocker = universe - candidate
            blockers.append(blocker)
            record("hitting_set", tuple(sorted(candidate)), result.status,
                   result.nodes, "sat_blocker_added",
                   f"candidate SAT; any core must hit {sorted(blocker)}")
        else:
            record("hitting_set", tuple(sorted(candidate)), result.status,
                   result.nodes, "undecided",
                   f"budget exhausted; valid lower bound is {lower_bound}")
            return CoreResult(KIND_UNDECIDED, (), records,
                              lower_bound=lower_bound, undecided=True,
                              total_nodes=budget.spent)
    return CoreResult(KIND_UNDECIDED, (), records, undecided=True,
                      total_nodes=budget.spent)
