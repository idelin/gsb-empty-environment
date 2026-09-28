from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass


@dataclass(frozen=True)
class Removal:
    variable: str
    value: object
    level: int


class DomainStore:
    """Finite domains with one rollback/commit trail per nesting level."""

    def __init__(self, domains: dict[str, Iterable[object]]):
        if not isinstance(domains, dict):
            raise TypeError("domains must be a dictionary")
        self._domains: dict[str, frozenset[object]] = {}
        for variable, values in domains.items():
            try:
                domain = frozenset(values)
            except TypeError as exc:
                raise TypeError(f"domain for {variable!r} must be finite iterable") from exc
            if not domain:
                raise ValueError(f"domain for {variable!r} must not be empty")
            self._domains[variable] = domain
        self._initial = dict(self._domains)
        self._trails: list[list[Removal]] = [[]]

    @property
    def level(self) -> int:
        return len(self._trails) - 1

    def variables(self) -> tuple[str, ...]:
        return tuple(self._domains)

    def initial_domain(self, variable: str) -> frozenset[object]:
        return self._initial[variable]

    def domain(self, variable: str) -> frozenset[object]:
        return self._domains[variable]

    def domains(self) -> dict[str, frozenset[object]]:
        return dict(self._domains)

    def push_level(self) -> int:
        self._trails.append([])
        return self.level

    def pop_level(self) -> None:
        if len(self._trails) <= 1:
            raise RuntimeError("cannot pop base level")
        removals = self._trails.pop()
        for removal in reversed(removals):
            self._domains[removal.variable] = self._domains[removal.variable] | {removal.value}

    def remove(self, variable: str, value: object) -> bool:
        current = self._domains[variable]
        if value not in current:
            return False
        self._domains[variable] = current - {value}
        self._trails[-1].append(Removal(variable, value, self.level))
        return True

    def removals_by_level(self) -> dict[int, list[Removal]]:
        result: dict[int, list[Removal]] = {}
        for trail in self._trails:
            for removal in trail:
                result.setdefault(removal.level, []).append(removal)
        return result
