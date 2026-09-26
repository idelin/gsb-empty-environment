"""Data model for the budgeted project-selection problem.

All numeric data is synthetic: ints or rationals (fractions.Fraction).
Costs must be non-negative; profits may be negative (a dependency with
negative profit can still be pulled in by a high-profit dependent).
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import Iterable


Number = int | Fraction


def as_fraction(value: Number) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):  # guard: bool is an int subclass
        raise TypeError("boolean is not a valid number here")
    if isinstance(value, int):
        return Fraction(value)
    raise TypeError(f"expected int or Fraction, got {type(value).__name__}")


@dataclass(frozen=True)
class Project:
    """A selectable project.

    deps: projects that MUST also be selected when this one is selected
    (selecting a child without its parent is illegal).
    """

    pid: str
    cost: Fraction
    profit: Fraction
    deps: tuple[str, ...] = ()

    def __init__(self, pid: str, cost: Number, profit: Number,
                 deps: Iterable[str] = ()):
        object.__setattr__(self, "pid", pid)
        object.__setattr__(self, "cost", as_fraction(cost))
        object.__setattr__(self, "profit", as_fraction(profit))
        object.__setattr__(self, "deps", tuple(deps))
        if self.cost < 0:
            raise ValueError(f"project {pid!r} has negative cost")


@dataclass
class Instance:
    """A problem instance: projects, mutual-exclusion pairs, budget."""

    projects: dict[str, Project]
    mutex: set[frozenset] = field(default_factory=set)  # pairs of pids
    budget: Fraction = Fraction(0)
    required: frozenset = frozenset()  # pids that must be selected

    def __init__(self,
                 projects: Iterable[Project],
                 mutex: Iterable[tuple[str, str]] = (),
                 budget: Number = 0,
                 required: Iterable[str] = ()):
        plist = list(projects)
        self.projects = {p.pid: p for p in plist}
        if len(self.projects) != len(plist):
            raise ValueError("duplicate project id")
        self.mutex = set()
        for a, b in mutex:
            if a == b:
                raise ValueError(f"self mutex on {a!r}")
            self._check_known(a, b)
            self.mutex.add(frozenset((a, b)))
        self.budget = as_fraction(budget)
        if self.budget < 0:
            raise ValueError("negative budget")
        self.required = frozenset(required)
        self._check_known(*self.required)
        for p in plist:
            self._check_known(*p.deps)

    def _check_known(self, *pids: str) -> None:
        for pid in pids:
            if pid not in self.projects:
                raise ValueError(f"unknown project id: {pid!r}")
