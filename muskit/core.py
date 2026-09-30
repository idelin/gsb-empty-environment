"""Deletion-based extraction of a subset-minimal conflicting core.

For every candidate item the budgeted internal solver is asked whether the
current core *without* that item is still conflicting:

* UNSAT   -> the item is redundant and removed (the log records the proof cost);
* SAT     -> the item is necessary; the satisfying assignment is stored as a
             per-item deletion witness and re-validated by the independent
             checker against the original constraints;
* UNKNOWN -> the internal solver ran out of budget.  This is propagated as
             *undecided*: the item is kept, but the final core may not be
             called subset-minimal.

Only when every surviving item has a currently valid deletion witness (i.e.
removing it makes the remaining set satisfiable) is the result reported as
SUBSET_MINIMAL.  Subset-minimal does NOT imply minimum cardinality.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Callable, Iterable, Optional

from .checker import check_assignment
from .constraints import Problem
from .solver import SAT, UNKNOWN, UNSAT, SolveResult

SolveFn = Callable[[Iterable[str], Optional[int]], SolveResult]

SUBSET_MINIMAL = "SUBSET_MINIMAL"
UNDECIDED = "UNDECIDED"
NOT_CONFLICTING = "NOT_CONFLICTING"


@dataclass(frozen=True)
class DeletionRecord:
    cid: str
    trial: tuple[str, ...]           # assumption set that was actually solved
    trial_status: str                # SAT / UNSAT / UNKNOWN
    decision: str                    # "removed" | "kept-sat" | "kept-undecided"
    nodes: int                       # solver effort spent on this check
    witness: Optional[dict[str, Any]]  # satisfying assignment when SAT


@dataclass
class CoreResult:
    core: list[str]
    status: str                      # SUBSET_MINIMAL | UNDECIDED | NOT_CONFLICTING
    log: list[DeletionRecord] = field(default_factory=list)
    witnesses: dict[str, dict[str, Any]] = field(default_factory=dict)

    def deletion_witness(self, cid: str) -> dict[str, Any]:
        return self.witnesses[cid]


def extract_subset_minimal_core(
    problem: Problem,
    solve: SolveFn,
    constraint_ids: Optional[Iterable[str]] = None,
    budget_per_check: Optional[int] = None,
) -> CoreResult:
    ids = list(constraint_ids) if constraint_ids is not None else problem.constraint_ids()

    initial = solve(ids, budget_per_check)
    if initial.status == SAT:
        bad = check_assignment(problem, ids, initial.assignment or {})
        if bad:
            raise AssertionError(f"solver claimed SAT but checker rejects: {bad}")
        return CoreResult(core=ids, status=NOT_CONFLICTING)
    if initial.status == UNKNOWN:
        # Budget exhausted on the full set: nothing may be concluded.
        return CoreResult(core=ids, status=UNDECIDED)

    core = list(ids)
    log: list[DeletionRecord] = []
    witnesses: dict[str, dict[str, Any]] = {}
    undecided = False

    for cid in list(core):
        trial = [x for x in core if x != cid]
        result = solve(trial, budget_per_check)
        if result.status == UNSAT:
            log.append(DeletionRecord(cid, tuple(trial), UNSAT, "removed", result.nodes, None))
            core = trial
        elif result.status == SAT:
            witness = dict(result.assignment or {})
            bad = check_assignment(problem, trial, witness)
            if bad:
                raise AssertionError(f"solver claimed SAT but checker rejects: {bad}")
            log.append(DeletionRecord(cid, tuple(trial), SAT, "kept-sat", result.nodes, witness))
            witnesses[cid] = witness
        else:  # UNKNOWN: budget exhausted -> propagate as undecided, keep item
            log.append(DeletionRecord(cid, tuple(trial), UNKNOWN, "kept-undecided", result.nodes, None))
            undecided = True

    if not undecided:
        # Minimality certificate: every surviving item must have a *currently
        # valid* deletion witness.  By monotonicity the recorded witness still
        # satisfies final_core \ {cid} (a subset of the trial set); verify it
        # independently against the original constraints anyway.
        for cid in core:
            remainder = [x for x in core if x != cid]
            bad = check_assignment(problem, remainder, witnesses[cid])
            if bad:
                raise AssertionError(f"stale deletion witness for {cid}: {bad}")

    return CoreResult(
        core=core,
        status=UNDECIDED if undecided else SUBSET_MINIMAL,
        log=log,
        witnesses=witnesses,
    )
