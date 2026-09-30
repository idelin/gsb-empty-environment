"""Independent SAT checker: validates a claimed solution against the
ORIGINAL problem constraints. Shares no state with the solver."""
from __future__ import annotations

from .model import Problem


def check_sat(problem: Problem, assignment: dict) -> bool:
    """True iff assignment is complete, in-domain, and satisfies every
    original constraint of the problem."""
    if not isinstance(assignment, dict):
        return False
    if set(assignment.keys()) != set(problem.variables):
        return False
    for var, val in assignment.items():
        if val not in problem.domains[var]:
            return False
    return all(c.satisfied(assignment) for c in problem.constraints)
