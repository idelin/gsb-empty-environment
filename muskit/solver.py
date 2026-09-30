"""Budgeted backtracking solver over finite domains (no SAT/SMT library).

Every search consumes a node budget (one node per attempted value).  When the
budget is exhausted the solver reports UNKNOWN; callers must not treat UNKNOWN
as SAT or UNSAT.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Iterable, Optional

from .constraints import Problem

SAT = "SAT"
UNSAT = "UNSAT"
UNKNOWN = "UNKNOWN"


@dataclass(frozen=True)
class SolveResult:
    status: str
    assignment: Optional[dict[str, Any]]
    nodes: int
    budget: Optional[int]

    @property
    def decisive(self) -> bool:
        return self.status in (SAT, UNSAT)


class _BudgetExhausted(Exception):
    pass


class BacktrackingSolver:
    def __init__(self, problem: Problem):
        self.problem = problem

    def solve(self, constraint_ids: Iterable[str], budget: Optional[int] = None) -> SolveResult:
        constraints = self.problem.pick(constraint_ids)
        var_names: list[str] = []
        for constraint in constraints:
            for name in constraint.var_names:
                if name not in var_names:
                    var_names.append(name)
        variables = [self.problem.variables[name] for name in var_names]
        variables.sort(key=lambda v: len(v.domain))  # smaller domains first

        remaining = budget
        used = 0
        assignment: dict[str, Any] = {}

        def consistent() -> bool:
            return not any(c.is_violated(assignment) for c in constraints)

        def search(idx: int) -> bool:
            nonlocal remaining, used
            if idx == len(variables):
                return True
            var = variables[idx]
            for value in var.domain:
                if remaining is not None:
                    if remaining <= 0:
                        raise _BudgetExhausted
                    remaining -= 1
                used += 1
                assignment[var.name] = value
                if consistent() and search(idx + 1):
                    return True
                del assignment[var.name]
            return False

        try:
            if search(0):
                return SolveResult(SAT, dict(assignment), used, budget)
            return SolveResult(UNSAT, None, used, budget)
        except _BudgetExhausted:
            return SolveResult(UNKNOWN, None, used, budget)
