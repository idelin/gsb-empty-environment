from __future__ import annotations

from collections.abc import Iterable

from .assumptions import Assumption
from .constraints import Constraint


def validate_assignment(
    assignment: object,
    initial_domains: dict[str, Iterable[object]],
    constraints: Iterable[Constraint],
    assumptions: Iterable[Assumption] = (),
) -> bool:
    """Check a claimed assignment against the original CSP and active assumptions."""

    if not isinstance(assignment, dict):
        return False

    initial = {variable: frozenset(values) for variable, values in initial_domains.items()}
    if set(assignment) != set(initial):
        return False

    for variable, value in assignment.items():
        if value not in initial[variable]:
            return False

    values = dict(assignment)
    for assumption in assumptions:
        if assumption.variable not in values or not assumption.allows(values[assumption.variable]):
            return False

    for constraint in constraints:
        if not set(constraint.variables) <= set(values):
            return False
        try:
            if not constraint.holds(values):
                return False
        except (KeyError, TypeError):
            return False

    return True
