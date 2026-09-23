"""Independent coloring verifier.

Deliberately shares no state or logic with the solver: it only looks at the
graph, a candidate coloring, and the original constraints.
"""

from __future__ import annotations


def verify_coloring(
    graph,
    coloring: dict[str, int],
    num_colors: int,
    capacities: dict[int, int] | None = None,
    precoloring: dict[str, int] | None = None,
) -> list[str]:
    """Return a list of human-readable violations; empty means valid."""
    problems: list[str] = []
    for v in sorted(graph.vertices):
        if v not in coloring:
            problems.append(f"vertex {v!r} has no color")
        elif not isinstance(coloring[v], int) or not 0 <= coloring[v] < num_colors:
            problems.append(
                f"vertex {v!r} color {coloring[v]!r} outside [0, {num_colors})"
            )
    for e in graph.edges:
        if e.u == e.v:
            problems.append(f"edge {e.id!r} is a self-loop on {e.u!r}")
            continue
        cu = coloring.get(e.u)
        cv = coloring.get(e.v)
        if cu is not None and cu == cv:
            problems.append(
                f"edge {e.id!r}: endpoints {e.u!r} and {e.v!r} share color {cu}"
            )
    for c, cap in sorted((capacities or {}).items()):
        count = sum(1 for v in graph.vertices if coloring.get(v) == c)
        if count > cap:
            problems.append(
                f"color {c} used {count} times, exceeding capacity {cap}"
            )
    for v, c in sorted((precoloring or {}).items()):
        if coloring.get(v) != c:
            problems.append(
                f"precolor violated: vertex {v!r} must be {c}, got {coloring.get(v)!r}"
            )
    return problems
