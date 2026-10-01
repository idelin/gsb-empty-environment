"""Problem definition for finite-domain integer minimization.

A problem consists of:
  - a finite, sorted domain of integers for each variable,
  - an integer linear objective to minimize,
  - integer linear constraints (<=, >=, ==).

Everything is integral, so all bounds computed by the solver are exact
integers and never suffer from floating-point drift.
"""
from __future__ import annotations

from dataclasses import dataclass
from enum import Enum


class Sense(str, Enum):
    LE = "<="
    GE = ">="
    EQ = "=="


@dataclass(frozen=True)
class LinearConstraint:
    """sum(coeffs[i] * x[i]) <sense> rhs."""

    coeffs: tuple[int, ...]
    sense: Sense
    rhs: int

    def __init__(self, coeffs, sense, rhs):
        object.__setattr__(self, "coeffs", tuple(int(c) for c in coeffs))
        object.__setattr__(self, "sense", Sense(sense))
        object.__setattr__(self, "rhs", int(rhs))


@dataclass(frozen=True)
class Problem:
    domains: tuple[tuple[int, ...], ...]
    objective: tuple[int, ...]
    constraints: tuple[LinearConstraint, ...]

    def __init__(self, domains, objective, constraints=()):
        norm_domains = tuple(
            tuple(sorted({int(v) for v in dom})) for dom in domains
        )
        for i, dom in enumerate(norm_domains):
            if not dom:
                raise ValueError(f"domain of variable {i} is empty")
        objective = tuple(int(c) for c in objective)
        if len(objective) != len(norm_domains):
            raise ValueError("objective length must match number of variables")
        constraints = tuple(constraints)
        for con in constraints:
            if len(con.coeffs) != len(norm_domains):
                raise ValueError("constraint arity must match number of variables")
        object.__setattr__(self, "domains", norm_domains)
        object.__setattr__(self, "objective", objective)
        object.__setattr__(self, "constraints", constraints)

    @property
    def num_vars(self) -> int:
        return len(self.domains)
