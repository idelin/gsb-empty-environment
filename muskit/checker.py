"""Independent assignment checker.

Shares no search code with the solver: it simply evaluates every constraint
predicate on a complete assignment and validates domains.  Whenever the solver
(or a recorded witness) claims SAT, the claim is re-validated here against the
*original* constraints.
"""
from __future__ import annotations

from typing import Any, Iterable

from .constraints import Problem


def check_assignment(
    problem: Problem,
    constraint_ids: Iterable[str],
    assignment: dict[str, Any],
) -> list[str]:
    """Return ids of violated/invalid constraints (empty list == claim holds)."""
    failures: list[str] = []
    for constraint in problem.pick(constraint_ids):
        for name in constraint.var_names:
            if name not in assignment:
                failures.append(constraint.cid)
                break
            if assignment[name] not in problem.variables[name].domain:
                failures.append(constraint.cid)
                break
        else:
            if constraint.is_violated(assignment):
                failures.append(constraint.cid)
    return failures


def is_satisfying(
    problem: Problem,
    constraint_ids: Iterable[str],
    assignment: dict[str, Any],
) -> bool:
    return not check_assignment(problem, constraint_ids, assignment)
