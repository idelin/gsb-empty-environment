"""Numeric triangular solve driven by a compiled dependency schedule.

Usage::

    solver = TriangularSolver(matrix)
    x = solver.solve(b)                 # L x = b (or U x = b)
    X = solver.solve_multi([b1, b2])    # many right-hand sides
    xt = solver.solve(b, transpose=True)  # L^T x = b / U^T x = b

Symbolic analysis (the dependency levels) is performed lazily and then
cached per ``(structure token, transpose)`` pair.  Many right-hand sides -
even across repeated calls - share one analysis as long as the matrix
structure is unchanged.  Changing stored numeric values keeps the token and
therefore reuses the analysis; adding/removing an off-diagonal entry changes
the token, the stale analysis is evicted and only that pattern is
re-analyzed.

Computation always follows the level waves.  Within a level nodes are
independent (that is the parallel partition); the loops are run sequentially
here only because the CPython stdlib offers no true numeric parallelism.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from . import analysis as symbolic
from .errors import DimensionError, ZeroPivotError
from .matrix import TriangularMatrix


@dataclass
class SolveStats:
    """Counters used to demonstrate (and test) analysis reuse."""

    solve_calls: int = 0
    right_hand_sides: int = 0
    analyses_performed: int = 0
    analyses_reused: int = 0
    local_invalidations: int = 0
    rejected_solves: int = 0

    @property
    def reuse_rate(self) -> float:
        total = self.analyses_performed + self.analyses_reused
        return 0.0 if total == 0 else self.analyses_reused / total

    def as_dict(self) -> dict:
        return {
            "solve_calls": self.solve_calls,
            "right_hand_sides": self.right_hand_sides,
            "analyses_performed": self.analyses_performed,
            "analyses_reused": self.analyses_reused,
            "analysis_reuse_rate": round(self.reuse_rate, 6),
            "local_invalidations": self.local_invalidations,
            "rejected_solves": self.rejected_solves,
        }


@dataclass(frozen=True)
class _CompiledNode:
    """Numeric plan for one node: pivot coordinate and its terms.

    A term references the coefficient at ``(r, c)`` in the *stored* matrix
    and the unknown component ``dep`` that it multiplies.
    """

    node: int
    pivot_row: int
    pivot_col: int
    terms: tuple[tuple[int, int, int], ...]  # (stored_row, stored_col, dep)


@dataclass(frozen=True)
class _CompiledPlan:
    levels: tuple[tuple[_CompiledNode, ...], ...]
    schedule: symbolic.LevelSchedule


class TriangularSolver:
    def __init__(self, matrix: TriangularMatrix, pivot_tol: float = 0.0) -> None:
        if pivot_tol < 0:
            raise ValueError("pivot_tol must be non-negative")
        self.matrix = matrix
        self.pivot_tol = float(pivot_tol)
        self.stats = SolveStats()
        # structure token -> {transpose flag: compiled plan}
        self._cache: dict[tuple, dict[bool, _CompiledPlan]] = {}

    # ------------------------------------------------------------- analysis

    def analyze(self, transpose: bool = False) -> symbolic.LevelSchedule:
        """Return the (cached) symbolic schedule, re-analyzing if structure changed."""
        return self._get_plan(transpose).schedule

    def cached_schedules(self) -> int:
        return sum(len(views) for views in self._cache.values())

    def _get_plan(self, transpose: bool) -> _CompiledPlan:
        token = self.matrix.structure_token
        views = self._cache.get(token)
        if views is None:
            # Local invalidation: this matrix grew a new structure; every
            # previously cached pattern is stale and gets evicted together.
            evicted = len(self._cache)
            if evicted:
                self.stats.local_invalidations += evicted
            self._cache.clear()
            views = {}
            self._cache[token] = views

        plan = views.get(transpose)
        if plan is None:
            schedule = symbolic.analyze(self.matrix, transpose)
            plan = self._compile(schedule)
            views[transpose] = plan
            self.stats.analyses_performed += 1
        else:
            self.stats.analyses_reused += 1
        return plan

    def _compile(self, schedule: symbolic.LevelSchedule) -> _CompiledPlan:
        compiled_levels = []
        for level in schedule.levels:
            nodes = []
            for node in level:
                terms = tuple(
                    (dep.coefficient_position[0], dep.coefficient_position[1], dep.depends_on)
                    for dep in schedule.dependency_evidence(node)
                )
                pr, pc = schedule.coefficient_positions[node]
                nodes.append(_CompiledNode(node, pr, pc, terms))
            compiled_levels.append(tuple(nodes))
        return _CompiledPlan(tuple(compiled_levels), schedule)

    # --------------------------------------------------------------- solve

    def solve(self, b, transpose: bool = False) -> list[float]:
        """Solve one right-hand side; returns a fresh list on success."""
        return self.solve_multi([b], transpose=transpose)[0]

    def solve_multi(self, rhs_list, transpose: bool = False) -> list[list[float]]:
        """Solve several right-hand sides sharing one symbolic analysis."""
        n = self.matrix.n
        columns = [list(col) for col in rhs_list]
        if not columns:
            return []
        for idx, col in enumerate(columns):
            if len(col) != n:
                raise DimensionError(
                    f"right-hand side {idx} has length {len(col)}, expected {n}"
                )

        self.stats.solve_calls += 1
        self.stats.right_hand_sides += len(columns)
        plan = self._get_plan(transpose)

        matrix = self.matrix
        unit = matrix.unit_diagonal
        tol = self.pivot_tol

        # Values are snapshotted once per call so an in-flight solve can never
        # observe a half-changed matrix.
        values = {(e.row, e.col): e.value for e in matrix.entries}
        diag = {
            i: (1.0 if unit else values[i, i])
            for i in range(n)
        }

        # Preflight every pivot before touching any unknown: zero pivots are a
        # numeric fault and the caller must not receive a half-solved column.
        bad = [i for i in range(n) if abs(diag[i]) <= tol]
        if bad:
            self.stats.rejected_solves += 1
            raise ZeroPivotError(bad, tol)

        # Scratch space only: committed to results after full success.
        x_cols = [list(col) for col in columns]
        for level in plan.levels:
            for node in level:
                for k in range(len(x_cols)):
                    acc = x_cols[k][node.node]
                    for r, c, dep in node.terms:
                        acc -= values[r, c] * x_cols[k][dep]
                    acc /= diag[node.node]
                    x_cols[k][node.node] = acc

        # All columns completed without a fault: publish now.
        return x_cols

    def solve_into(self, b, transpose: bool = False) -> None:
        """Solve in place; ``b`` is left untouched if the solve fails."""
        result = self.solve(b, transpose=transpose)
        b[:] = result
