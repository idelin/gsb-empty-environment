"""Subset-minimal conflict core extraction over the original constraints.

The constraint universe is: one constraint per edge (by edge id), one per
precoloring entry (by vertex id), one per capacity bound (by color).
Deletion-based minimization: a constraint is dropped permanently if the
problem stays infeasible without it. Minimality is only claimed when every
feasibility check completed within its budget; budget exhaustion anywhere
downgrades the result to "not proven minimal".
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional

from .model import Graph, Problem
from .solver import Solver, Status


@dataclass(frozen=True)
class ConstraintRef:
    kind: str  # "edge" | "precolor" | "capacity"
    key: str   # edge id | vertex id | color

    def __str__(self) -> str:
        return f"{self.kind}:{self.key}"


def all_constraints(problem: Problem) -> list[ConstraintRef]:
    refs = [ConstraintRef("edge", e.id) for e in problem.graph.edges]
    refs += [ConstraintRef("precolor", v) for v in sorted(problem.precoloring)]
    refs += [ConstraintRef("capacity", c) for c in sorted(problem.capacities)]
    return refs


def build_subproblem(problem: Problem, keep: set[ConstraintRef]) -> Problem:
    g = Graph()
    for v in problem.graph.vertices:
        g.add_vertex(v)  # isolated vertices are preserved
    for e in problem.graph.edges:
        if ConstraintRef("edge", e.id) in keep:
            g.add_edge(e.id, e.u, e.v)  # multi-edges kept by id
    pre = {v: c for v, c in problem.precoloring.items()
           if ConstraintRef("precolor", v) in keep}
    cap = {c: k for c, k in problem.capacities.items()
           if ConstraintRef("capacity", c) in keep}
    return Problem(g, problem.colors, cap, pre)


@dataclass
class CoreResult:
    status: Status
    core: Optional[list[ConstraintRef]] = None
    is_subset_minimal: bool = False
    checks: int = 0
    notes: list[str] = field(default_factory=list)


def minimal_conflict_core(
    problem: Problem,
    max_nodes_per_check: Optional[int] = None,
) -> CoreResult:
    """Find a subset-minimal infeasible core of the original constraints.

    is_subset_minimal is True only if the full problem was proven UNSAT and
    every sub-check also terminated (SAT or UNSAT) within budget.
    """
    notes: list[str] = []
    checks = 0

    def feasible_status(p: Problem) -> Status:
        nonlocal checks
        checks += 1
        return Solver(p, max_nodes=max_nodes_per_check).solve().status

    full = feasible_status(problem)
    if full is Status.SAT:
        return CoreResult(Status.SAT, None, False, checks,
                          ["problem is feasible; no conflict core exists"])
    if full is Status.BUDGET_EXHAUSTED:
        return CoreResult(Status.BUDGET_EXHAUSTED, None, False, checks,
                          ["budget exhausted on the full problem; "
                           "infeasibility not established, no core claimed"])

    keep = set(all_constraints(problem))
    minimal = True
    for ref in list(keep):
        trial = keep - {ref}
        st = feasible_status(build_subproblem(problem, trial))
        if st is Status.UNSAT:
            keep = trial
        elif st is Status.BUDGET_EXHAUSTED:
            minimal = False
            notes.append(f"budget exhausted while testing {ref}; kept")
    core = sorted(keep, key=lambda r: (r.kind, r.key))
    if not minimal:
        notes.append("core is infeasible but NOT proven subset-minimal")
    return CoreResult(Status.UNSAT, core, minimal, checks, notes)
