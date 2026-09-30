"""Solve cache bound to the assumption-set version.

Keys are (problem revision, budget, frozenset of constraint ids).  Mutating
the constraint pool bumps the revision, so stale entries become unreachable;
UNKNOWN entries are budget-specific and can never poison a higher-budget query.
"""
from __future__ import annotations

from typing import Iterable, Optional

from .constraints import Problem
from .solver import BacktrackingSolver, SolveResult


class CachedSolver:
    def __init__(self, problem: Problem, solver: Optional[BacktrackingSolver] = None):
        self.problem = problem
        self.solver = solver or BacktrackingSolver(problem)
        self._entries: dict[tuple, SolveResult] = {}
        self.hits = 0
        self.misses = 0

    def _key(self, constraint_ids: Iterable[str], budget: Optional[int]) -> tuple:
        return (self.problem.revision, budget, frozenset(constraint_ids))

    def solve(self, constraint_ids: Iterable[str], budget: Optional[int] = None) -> SolveResult:
        key = self._key(constraint_ids, budget)
        if key in self._entries:
            self.hits += 1
            return self._entries[key]
        self.misses += 1
        result = self.solver.solve(sorted(key[2]), budget)
        self._entries[key] = result
        return result

    def invalidate(self) -> None:
        self._entries.clear()

    @property
    def size(self) -> int:
        return len(self._entries)
