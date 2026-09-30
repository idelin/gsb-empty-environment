"""Convenience builders for common constraint shapes."""

from __future__ import annotations

import operator
from typing import Any, Callable, Iterable

from .model import Constraint

_OPS: dict[str, Callable[[Any, Any], bool]] = {
    "<=": operator.le,
    "<": operator.lt,
    "==": operator.eq,
    "!=": operator.ne,
    ">=": operator.ge,
    ">": operator.gt,
}


def linear_constraint(
    name: str,
    coeffs: dict[str, int | float],
    sense: str,
    rhs: int | float,
    *,
    hard: bool = True,
    weight: int = 0,
    level: int = 0,
) -> Constraint:
    """Build ``sum(coeffs[v] * x_v) <sense> rhs`` as a constraint.

    Domains of the referenced variables must be numeric.
    """
    if sense not in _OPS:
        raise ValueError(f"unsupported sense {sense!r}; use one of {sorted(_OPS)}")
    op = _OPS[sense]
    terms = dict(coeffs)

    def predicate(assignment: dict[str, Any]) -> bool:
        total = sum(c * assignment[v] for v, c in terms.items())
        return op(total, rhs)

    return Constraint(
        name=name,
        scope=tuple(terms),
        predicate=predicate,
        hard=hard,
        weight=weight,
        level=level,
    )


def all_different(
    name: str,
    var_names: Iterable[str],
    *,
    hard: bool = True,
    weight: int = 0,
    level: int = 0,
) -> Constraint:
    """Build a constraint requiring all listed variables to take distinct values."""
    names = tuple(var_names)
    if len(names) < 2:
        raise ValueError("all_different needs at least two variables")

    def predicate(assignment: dict[str, Any]) -> bool:
        values = [assignment[v] for v in names]
        return len(set(values)) == len(values)

    return Constraint(
        name=name,
        scope=names,
        predicate=predicate,
        hard=hard,
        weight=weight,
        level=level,
    )
