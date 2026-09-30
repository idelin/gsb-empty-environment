"""Finite-domain constraint model with hard and weighted soft constraints.

Semantics
---------
- Hard constraints must be satisfied by every returned solution. They carry
  no weight and can never be "bought through" by penalties.
- Soft constraints carry a positive integer weight and a hierarchy level.
  Violating one adds ``weight`` to the objective component at its level.
- Levels are minimized lexicographically: level 0 is the highest priority.
  No amount of lower-level gain can compensate for a higher-level penalty,
  because objective vectors are compared as tuples.
- Negative (and zero) soft weights are rejected at model construction time.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Callable, Iterable

Predicate = Callable[[dict[str, Any]], bool]


@dataclass(frozen=True)
class Variable:
    """A decision variable with a finite, non-empty domain."""

    name: str
    domain: tuple[Any, ...]

    def __post_init__(self) -> None:
        if len(self.domain) == 0:
            raise ValueError(
                f"variable {self.name!r} must have a non-empty finite domain"
            )


@dataclass(frozen=True)
class Constraint:
    """A constraint over a scope of variable names.

    ``predicate`` receives an assignment mapping and returns True when the
    constraint is satisfied. It is only evaluated once every variable in
    ``scope`` is assigned.
    """

    name: str
    scope: tuple[str, ...]
    predicate: Predicate
    hard: bool
    weight: int
    level: int

    def __post_init__(self) -> None:
        if self.hard:
            if self.weight != 0:
                raise ValueError(
                    f"hard constraint {self.name!r} must have weight 0; "
                    "hard constraints cannot be traded against penalties"
                )
        else:
            if isinstance(self.weight, bool) or not isinstance(self.weight, int):
                raise TypeError(
                    f"soft constraint {self.name!r}: weight must be an int, "
                    f"got {type(self.weight).__name__}"
                )
            if self.weight <= 0:
                raise ValueError(
                    f"soft constraint {self.name!r}: weight must be a positive "
                    f"integer, got {self.weight}; negative or zero weights are "
                    "rejected because penalties must be non-negative for the "
                    "lexicographic lower-bound reasoning to be sound"
                )
        if isinstance(self.level, bool) or not isinstance(self.level, int):
            raise TypeError(
                f"constraint {self.name!r}: level must be an int, "
                f"got {type(self.level).__name__}"
            )
        if self.level < 0:
            raise ValueError(
                f"constraint {self.name!r}: level must be >= 0, got {self.level}"
            )


class Model:
    """A finite-domain optimization model."""

    def __init__(self) -> None:
        self._variables: dict[str, Variable] = {}
        self._constraints: list[Constraint] = []

    def add_variable(self, name: str, domain: Iterable[Any]) -> Variable:
        if name in self._variables:
            raise ValueError(f"duplicate variable name {name!r}")
        variable = Variable(name=name, domain=tuple(domain))
        self._variables[name] = variable
        return variable

    def add_constraint(self, constraint: Constraint) -> Constraint:
        unknown = [n for n in constraint.scope if n not in self._variables]
        if unknown:
            raise ValueError(
                f"constraint {constraint.name!r} references unknown "
                f"variable(s): {unknown}"
            )
        if any(c.name == constraint.name for c in self._constraints):
            raise ValueError(f"duplicate constraint name {constraint.name!r}")
        self._constraints.append(constraint)
        return constraint

    def add_hard(self, name: str, scope: Iterable[str], predicate: Predicate) -> Constraint:
        return self.add_constraint(
            Constraint(name, tuple(scope), predicate, hard=True, weight=0, level=0)
        )

    def add_soft(
        self,
        name: str,
        scope: Iterable[str],
        predicate: Predicate,
        *,
        weight: int,
        level: int,
    ) -> Constraint:
        return self.add_constraint(
            Constraint(name, tuple(scope), predicate, hard=False, weight=weight, level=level)
        )

    @property
    def variables(self) -> tuple[Variable, ...]:
        return tuple(self._variables.values())

    @property
    def constraints(self) -> tuple[Constraint, ...]:
        return tuple(self._constraints)

    @property
    def num_levels(self) -> int:
        soft_levels = [c.level for c in self._constraints if not c.hard]
        return max(soft_levels) + 1 if soft_levels else 0
