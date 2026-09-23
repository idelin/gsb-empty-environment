"""Independent brute-force enumerator used to cross-check solver pruning.

Uses itertools.product plus the independent verifier, so it shares no
propagation or search logic with the solver. Suitable for small graphs.
"""

from __future__ import annotations

from itertools import product

from .verifier import verify_coloring


def enumerate_all_colorings(
    graph,
    num_colors: int,
    capacities: dict[int, int] | None = None,
    precoloring: dict[str, int] | None = None,
    limit: int = 1_000_000,
):
    """Return (solutions, truncated). Each solution is {vertex_id: color}."""
    vertices = sorted(graph.vertices)
    pre = precoloring or {}
    domains = [
        [pre[v]] if v in pre else list(range(num_colors)) for v in vertices
    ]
    solutions: list[dict[str, int]] = []
    truncated = False
    for combo in product(*domains):
        coloring = dict(zip(vertices, combo))
        if not verify_coloring(graph, coloring, num_colors, capacities, precoloring):
            solutions.append(coloring)
            if len(solutions) >= limit:
                truncated = True
                break
    return solutions, truncated


def canonical_form(coloring: dict[str, int], vertices: list[str]) -> tuple[int, ...]:
    """Color-permutation canonical form: colors renamed by first occurrence
    when scanning `vertices` in order."""
    mapping: dict[int, int] = {}
    out: list[int] = []
    for v in vertices:
        c = coloring[v]
        if c not in mapping:
            mapping[c] = len(mapping)
        out.append(mapping[c])
    return tuple(out)


def canonical_set(solutions, vertices: list[str]) -> set[tuple[int, ...]]:
    return {canonical_form(s, vertices) for s in solutions}
