"""Solver wrapper: result cache keyed by assumption-set version + SAT checking.

The cache key is (version, frozenset(assumption_indices)): results are
bound to an explicit assumption-set version, and invalidate() bumps the
version so stale entries can never be served after the constraint pool
changes. Every SAT answer is validated by the independent checker against
the original constraints before it is cached or returned.
"""

from __future__ import annotations

from typing import Optional, Sequence

from .checker import check_assignment
from .solver import Problem, SolveResult, Status, solve


class InternalSolverError(RuntimeError):
    """Raised when the solver claims SAT but the independent checker disagrees."""


class CachedSolver:
    def __init__(self, problem: Problem, per_call_budget: int = 5_000) -> None:
        self.problem = problem
        self.per_call_budget = per_call_budget
        self.version = 0
        self._store: dict = {}
        self.hits = 0
        self.misses = 0
        self.solver_calls = 0

    def invalidate(self) -> None:
        """Bump the assumption-set version and drop all cached results."""
        self.version += 1
        self._store.clear()

    def solve(
        self, indices: Sequence[int], budget: Optional[int] = None
    ) -> SolveResult:
        key = (self.version, frozenset(indices))
        if key in self._store:
            self.hits += 1
            return self._store[key]
        self.misses += 1
        self.solver_calls += 1
        result = solve(
            self.problem,
            tuple(sorted(indices)),
            budget=self.per_call_budget if budget is None else budget,
        )
        if result.status is Status.SAT:
            errors = check_assignment(self.problem, tuple(indices), result.witness)
            if errors:
                raise InternalSolverError(
                    "solver claimed SAT but the independent checker rejected "
                    f"the witness: {errors}"
                )
        self._store[key] = result
        return result
