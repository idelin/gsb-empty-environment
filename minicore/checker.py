"""Independent solution checker.

Deliberately separate from the solver: it re-evaluates the *original*
constraint predicates against a claimed witness. Whenever the solver (or
any caller) claims SAT, the claim is only trusted after this checker
confirms the witness against the original constraints.
"""

from __future__ import annotations

from typing import Mapping, Sequence

from .solver import Problem


def check_assignment(
    problem: Problem,
    indices: Sequence[int],
    assignment: Mapping[str, int],
) -> list[str]:
    """Return a list of violation messages; empty means the claim is valid."""
    errors: list[str] = []
    cons = [problem.constraints[i] for i in indices]
    needed = {v for con in cons for v in con.variables}
    for var in sorted(needed):
        if var not in assignment:
            errors.append(f"variable {var!r} is unassigned")
        elif assignment[var] not in problem.domains[var]:
            errors.append(
                f"variable {var!r}={assignment[var]!r} outside domain "
                f"{tuple(problem.domains[var])!r}"
            )
    for con in cons:
        if all(v in assignment for v in con.variables) and not con.holds(assignment):
            errors.append(f"constraint {con.name!r} violated by {dict(assignment)!r}")
    return errors
