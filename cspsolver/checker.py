"""Independent solution checker.

This module deliberately shares no code with the search procedure. It takes
the *original* model and a candidate assignment, and re-derives everything
from scratch: domain membership, hard-constraint satisfaction, and the exact
lexicographic penalty vector. The solver calls it before claiming a solution.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, List, Tuple

from .model import Model


@dataclass(frozen=True)
class Violation:
    constraint: str
    level: int
    weight: int
    scope_values: Dict[str, int]


@dataclass(frozen=True)
class CheckResult:
    hard_ok: bool
    hard_failures: List[str]
    penalties: Tuple[int, ...]
    violations: List[Violation]


def check_assignment(model: Model, assignment: Dict[str, int]) -> CheckResult:
    missing = [v for v in model.variables if v not in assignment]
    if missing:
        raise ValueError(f"incomplete assignment, missing variables: {missing}")
    extra = [k for k in assignment if k not in model.variables]
    if extra:
        raise ValueError(f"assignment contains unknown variables: {extra}")
    for name, var in model.variables.items():
        if assignment[name] not in var.domain:
            raise ValueError(
                f"variable {name!r} assigned {assignment[name]!r} "
                f"outside its domain {var.domain!r}"
            )

    hard_failures: List[str] = []
    for c in model.hard:
        vals = tuple(assignment[v] for v in c.var_names)
        if not c.predicate(*vals):
            hard_failures.append(c.name)

    penalties = [0] * model.num_levels
    violations: List[Violation] = []
    for c in model.soft:
        vals = tuple(assignment[v] for v in c.var_names)
        if not c.predicate(*vals):
            penalties[c.level] += c.weight
            violations.append(
                Violation(
                    constraint=c.name,
                    level=c.level,
                    weight=c.weight,
                    scope_values={v: assignment[v] for v in c.var_names},
                )
            )
    return CheckResult(
        hard_ok=not hard_failures,
        hard_failures=hard_failures,
        penalties=tuple(penalties),
        violations=violations,
    )
