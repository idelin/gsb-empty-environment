from __future__ import annotations

from collections.abc import Callable, Sequence
from dataclasses import dataclass


Predicate = Callable[..., bool]


@dataclass(frozen=True)
class Constraint:
    variables: tuple[str, ...]
    predicate: Predicate
    name: str

    def __init__(self, variables: Sequence[str], predicate: Predicate, name: str):
        object.__setattr__(self, "variables", tuple(variables))
        object.__setattr__(self, "predicate", predicate)
        object.__setattr__(self, "name", name)

    def holds(self, values: dict[str, object]) -> bool:
        return bool(self.predicate(*(values[variable] for variable in self.variables)))
