"""Symbolic analysis: dependency-driven level scheduling for triangular solves.

The effective system is always written as a lower-triangular forward solve.
For a stored lower matrix ``L`` solving ``L x = b``::

    x_i = (b_i - sum_{j < i, L_ij != 0} L_ij x_j) / L_ii

so a stored entry ``(r, c)`` with ``r > c`` makes node ``r`` depend on node
``c`` and its coefficient is ``L[r, c]``.

For a stored upper matrix ``U`` solving ``U x = b``::

    x_i = (b_i - sum_{j > i, U_ij != 0} U_ij x_j) / U_ii

so a stored entry ``(r, c)`` with ``r < c`` makes node ``r`` depend on node
``c`` and its coefficient is ``U[r, c]``.

Transposition is handled as a *view*: requesting ``L^T x = b`` turns a lower
matrix into an effective upper solve, and vice versa, without copying or
reordering storage.  The scheduling order is decided solely by these numeric
dependencies - the order in which coordinates were supplied is irrelevant.

Nodes whose dependencies have all been computed at earlier levels form one
level; every node in a level is an independent parallel task.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .errors import StructureError
from .matrix import LOWER, UPPER, TriangularMatrix


@dataclass(frozen=True)
class Dependency:
    """Evidence that node ``node`` waits for node ``depends_on``.

    ``coefficient_position`` is the entry of the stored matrix that creates
    the edge (and the value the numeric kernel must multiply by).
    """

    node: int
    depends_on: int
    coefficient_position: tuple[int, int]


@dataclass
class LevelSchedule:
    """Result of symbolic analysis for one (matrix, transpose) combination."""

    n: int
    uplo_effective: str
    transpose: bool
    levels: list[list[int]]
    dependencies: dict[int, list[Dependency]]
    coefficient_positions: dict[int, tuple[int, int]] = field(default_factory=dict)

    @property
    def depth(self) -> int:
        """Number of sequential level waves (critical path length)."""
        return len(self.levels)

    def parallel_nodes(self) -> list[list[int]]:
        """Nodes that can execute concurrently at each dependency level."""
        return self.levels

    def dependency_evidence(self, node: int) -> list[Dependency]:
        return list(self.dependencies.get(node, ()))

    def summary(self) -> str:
        widths = ", ".join(str(len(level)) for level in self.levels)
        return (
            f"depth={self.depth} level widths=[{widths}] "
            f"uplo={self.uplo_effective} transpose={self.transpose}"
        )


def analyze(matrix: TriangularMatrix, transpose: bool = False) -> LevelSchedule:
    """Build the dependency level schedule using Kahn's longest-path layering.

    Each node enters level ``1 + max(level(dependency))`` (level 0 when it has
    no dependencies), so every edge goes from an earlier level to a later one.
    """
    n = matrix.n
    effective_lower = (matrix.uplo == LOWER) != transpose

    deps: dict[int, list[tuple[int, tuple[int, int]]]] = {i: [] for i in range(n)}
    for entry in matrix.off_diagonal:
        r, c = entry.row, entry.col
        # Coefficient is always read at the stored matrix coordinate.
        coeff = (r, c)
        if not transpose:
            # Stored entry is a coefficient in node r's own row.
            node, dep = r, c
        else:
            # The effective system reads the transposed entry at (c, r):
            # it is a coefficient in node c's row multiplying x_r.
            node, dep = c, r
        deps[node].append((dep, coeff))

    indegree = [0] * n
    children: dict[int, list[int]] = {i: [] for i in range(n)}
    evidence: dict[int, list[Dependency]] = {i: [] for i in range(n)}
    coeff_positions = {i: (i, i) for i in range(n)}

    for node, incoming in deps.items():
        indegree[node] = len(incoming)
        for dep, coeff in incoming:
            children[dep].append(node)
            evidence[node].append(Dependency(node, dep, coeff))
        incoming.sort()
        evidence[node].sort(key=lambda d: d.depends_on)

    # Kahn traversal, assigning each node its dependency depth.
    level_of = [-1] * n
    ready = sorted(i for i in range(n) if indegree[i] == 0)
    scheduled = 0
    while ready:
        next_ready: list[int] = []
        for node in ready:
            level_of[node] = (
                0 if not deps[node] else 1 + max(level_of[d] for d, _ in deps[node])
            )
            scheduled += 1
            for child in children[node]:
                indegree[child] -= 1
                if indegree[child] == 0:
                    next_ready.append(child)
        ready = sorted(next_ready)

    if scheduled != n:
        cyclic = [i for i in range(n) if level_of[i] < 0]
        raise StructureError(
            f"dependency graph is cyclic; unresolved nodes: {cyclic[:10]}"
        )

    depth = max(level_of, default=-1) + 1
    levels: list[list[int]] = [[] for _ in range(depth)]
    for node, lvl in enumerate(level_of):
        levels[lvl].append(node)
    for level in levels:
        level.sort()

    effective_uplo = LOWER if effective_lower else UPPER
    return LevelSchedule(
        n=n,
        uplo_effective=effective_uplo,
        transpose=transpose,
        levels=levels,
        dependencies=evidence,
        coefficient_positions=coeff_positions,
    )
