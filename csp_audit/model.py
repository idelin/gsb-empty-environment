"""CSP model: finite-domain variables and predicate constraints."""
from __future__ import annotations

from dataclasses import dataclass
from itertools import product
from typing import Callable


@dataclass(frozen=True)
class Constraint:
    """A constraint over an ordered scope of variables.

    predicate receives a tuple of values in scope order and returns bool.
    """

    cid: str
    scope: tuple[str, ...]
    predicate: Callable[[tuple], bool]

    def satisfied(self, assignment: dict) -> bool:
        """True iff every scope variable is assigned and the predicate holds."""
        if any(v not in assignment for v in self.scope):
            return False
        return bool(self.predicate(tuple(assignment[v] for v in self.scope)))

    def has_support(self, var: str, value, domains: dict[str, set]) -> bool:
        """True iff (var = value) can be extended to a satisfying tuple whose
        other values come from the given current domains (GAC support)."""
        if var not in self.scope:
            raise ValueError(f"{var} not in scope of {self.cid}")
        others = [v for v in self.scope if v != var]
        for combo in product(*(sorted(domains[v], key=repr) for v in others)):
            it = iter(combo)
            full = tuple(value if v == var else next(it) for v in self.scope)
            if self.predicate(full):
                return True
        return False


@dataclass
class Problem:
    variables: list[str]
    domains: dict[str, set]
    constraints: list[Constraint]

    def __post_init__(self):
        self.domains = {v: set(self.domains[v]) for v in self.variables}
        self._by_cid = {c.cid: c for c in self.constraints}
        if len(self._by_cid) != len(self.constraints):
            raise ValueError("duplicate constraint ids")

    def constraint(self, cid: str) -> Constraint | None:
        return self._by_cid.get(cid)
