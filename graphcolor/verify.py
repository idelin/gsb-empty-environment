"""Independent verifier: checks a coloring against a problem without
reusing any solver internals."""

from __future__ import annotations

from .model import Problem


def verify(problem: Problem, coloring: dict[str, str]) -> list[str]:
    """Return a list of human-readable violations; empty means valid."""
    problem.validate()
    violations: list[str] = []
    vertices = problem.graph.vertices
    vset = set(vertices)

    for v in vertices:
        if v not in coloring:
            violations.append(f"missing assignment for vertex {v}")
    for v in coloring:
        if v not in vset:
            violations.append(f"assignment for unknown vertex {v}")

    known = set(problem.colors)
    for v, c in coloring.items():
        if c not in known:
            violations.append(f"vertex {v} uses unknown color {c}")

    for e in problem.graph.edges:
        cu, cv = coloring.get(e.u), coloring.get(e.v)
        if cu is not None and cu == cv:
            violations.append(
                f"edge {e.id}: endpoints {e.u} and {e.v} share color {cu}")

    for c, k in problem.capacities.items():
        n = sum(1 for x in coloring.values() if x == c)
        if n > k:
            violations.append(f"color {c} used {n} times, capacity is {k}")

    for v, c in problem.precoloring.items():
        if coloring.get(v) != c:
            violations.append(
                f"precoloring violated: {v} must be {c}, got {coloring.get(v)}")

    return violations
