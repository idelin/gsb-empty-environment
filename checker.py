"""Independent solution checker.

This module is deliberately separate from the solver and shares no code
with the search internals.  It re-evaluates the *original* constraints
and domains directly, so a bug in the solver's pruning/bounding logic
cannot make an infeasible assignment pass verification.

The solver must call `verify_solution` on any assignment it claims is a
feasible solution before returning it.
"""
from __future__ import annotations

from model import Problem, Sense


def check_assignment(problem: Problem, assignment) -> list[str]:
    """Return a list of human-readable violations; empty means feasible."""
    errors: list[str] = []
    assignment = tuple(assignment)
    if len(assignment) != problem.num_vars:
        return [
            f"assignment has {len(assignment)} values, "
            f"expected {problem.num_vars}"
        ]
    for i, (value, domain) in enumerate(zip(assignment, problem.domains)):
        if value not in domain:
            errors.append(f"x[{i}] = {value} is not in domain {domain}")
    for j, con in enumerate(problem.constraints):
        activity = sum(a * x for a, x in zip(con.coeffs, assignment))
        if con.sense is Sense.LE and activity > con.rhs:
            errors.append(
                f"constraint {j} violated: {activity} <= {con.rhs} is false"
            )
        elif con.sense is Sense.GE and activity < con.rhs:
            errors.append(
                f"constraint {j} violated: {activity} >= {con.rhs} is false"
            )
        elif con.sense is Sense.EQ and activity != con.rhs:
            errors.append(
                f"constraint {j} violated: {activity} == {con.rhs} is false"
            )
    return errors


def objective_value(problem: Problem, assignment) -> int:
    """Evaluate the original objective on a complete assignment."""
    if len(assignment) != problem.num_vars:
        raise ValueError("assignment length does not match number of variables")
    return sum(c * x for c, x in zip(problem.objective, assignment))


def verify_solution(problem: Problem, assignment) -> tuple[bool, int | None]:
    """Independent re-check of a claimed solution.

    Returns (is_feasible, objective_value).  The objective is None when
    the assignment is infeasible.
    """
    errors = check_assignment(problem, assignment)
    if errors:
        return False, None
    return True, objective_value(problem, assignment)
