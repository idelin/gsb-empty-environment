from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass


@dataclass(frozen=True)
class Assumption:
    variable: str
    values: frozenset[object]

    def __init__(self, variable: str, values: Iterable[object] | object):
        if isinstance(values, Iterable) and not isinstance(values, (str, bytes, bytearray)):
            allowed = frozenset(values)
        else:
            allowed = frozenset({values})
        if not allowed:
            raise ValueError("assumption must retain at least one value")
        object.__setattr__(self, "variable", variable)
        object.__setattr__(self, "values", allowed)

    def allows(self, value: object) -> bool:
        return value in self.values

    def restriction(self, value: object) -> bool:
        return self.allows(value)
