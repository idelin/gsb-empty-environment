"""Finite-domain constraint problem model."""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Callable, Iterable


@dataclass(frozen=True)
class Variable:
    name: str
    domain: tuple[Any, ...]

    def __init__(self, name: str, domain: Iterable[Any]):
        object.__setattr__(self, "name", name)
        object.__setattr__(self, "domain", tuple(domain))
        if not self.domain:
            raise ValueError(f"variable {name!r} has an empty domain")


Predicate = Callable[..., bool]


@dataclass(frozen=True)
class Constraint:
    cid: str
    var_names: tuple[str, ...]
    predicate: Predicate
    description: str = ""

    def is_violated(self, assignment: dict[str, Any]) -> bool:
        """True only when every variable is assigned and the predicate fails."""
        if any(v not in assignment for v in self.var_names):
            return False
        return not self.predicate(*(assignment[v] for v in self.var_names))


class Problem:
    """A pool of named constraints over finite-domain variables.

    `revision` is bumped on every mutation of the constraint pool; caches are
    keyed on it so stale assumption-set results can never be reused.
    """

    def __init__(self, variables: Iterable[Variable], constraints: Iterable[Constraint] = ()):
        self.variables = {v.name: v for v in variables}
        self.constraints: dict[str, Constraint] = {}
        self.revision = 0
        for constraint in constraints:
            self.add_constraint(constraint)

    def add_constraint(self, constraint: Constraint) -> None:
        for name in constraint.var_names:
            if name not in self.variables:
                raise KeyError(f"constraint {constraint.cid!r} references unknown variable {name!r}")
        if constraint.cid in self.constraints:
            raise KeyError(f"duplicate constraint id {constraint.cid!r}")
        self.constraints[constraint.cid] = constraint
        self.revision += 1

    def remove_constraint(self, cid: str) -> None:
        del self.constraints[cid]
        self.revision += 1

    def constraint_ids(self) -> list[str]:
        return list(self.constraints)

    def pick(self, ids: Iterable[str]) -> list[Constraint]:
        return [self.constraints[i] for i in ids]
