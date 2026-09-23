"""Pareto-optimal cost/delay simple paths under a cost budget.

Model
-----
* Directed multigraph. Vertices and edges carry string IDs.
  Isolated vertices are kept; parallel edges (multi-edges) between the
  same ordered pair of vertices are kept and distinguished by edge ID.
* Every edge has two non-negative weights: ``cost`` and ``delay``.
* Every edge has an availability time window ``[start, end)`` -- left
  closed, right open. An edge may be entered at vertex ``v`` only when
  the arrival delay at ``v`` satisfies ``start <= delay < end``.
  The default window is ``[0, +inf)`` (always available).
* Paths are simple: no vertex is repeated.
* A path is feasible when its total cost satisfies ``cost <= budget``
  (the budget boundary is inclusive).

Dominance
---------
Path A dominates path B iff ``A.cost <= B.cost`` and ``A.delay <= B.delay``
with at least one strict inequality. Only non-dominated feasible paths
(the Pareto frontier) are returned. Distinct paths with an identical
(cost, delay) pair do not dominate each other and are all kept.

Truncation
----------
``max_results`` caps the number of returned frontier paths and
``max_labels`` caps the search effort (number of partial-path labels
generated). If either cap cuts the enumeration short, the result is
marked ``truncated=True`` and ``is_complete`` is ``False``. A truncated
result is a best-effort partial frontier and must NOT be treated as the
complete Pareto frontier. With no feasible path the frontier is empty
and ``truncated`` is ``False`` (the empty result is complete).
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Iterator, Optional, Tuple

__all__ = ["Edge", "Graph", "Path", "SearchResult", "pareto_paths"]


@dataclass(frozen=True)
class Edge:
    """A directed edge with string ID and two non-negative weights."""

    id: str
    source: str
    target: str
    cost: float
    delay: float
    window: Tuple[float, float]  # [start, end): left closed, right open


class Graph:
    """Directed multigraph with string vertex/edge IDs."""

    def __init__(self) -> None:
        self._vertices: set = set()
        self._edges: dict = {}
        self._out: dict = {}

    def add_vertex(self, vertex_id: str) -> None:
        if not isinstance(vertex_id, str):
            raise TypeError("vertex id must be a string")
        self._vertices.add(vertex_id)
        self._out.setdefault(vertex_id, [])

    def add_edge(
        self,
        edge_id: str,
        source: str,
        target: str,
        cost: float,
        delay: float,
        window: Tuple[float, float] = (0.0, math.inf),
    ) -> None:
        if not isinstance(edge_id, str):
            raise TypeError("edge id must be a string")
        if edge_id in self._edges:
            raise ValueError(f"duplicate edge id: {edge_id!r}")
        cost = float(cost)
        delay = float(delay)
        if cost < 0:
            raise ValueError("cost must be non-negative")
        if delay < 0:
            raise ValueError("delay must be non-negative")
        start, end = float(window[0]), float(window[1])
        if start > end:
            raise ValueError("window start must not exceed window end")
        for vertex_id in (source, target):
            if vertex_id not in self._vertices:
                self.add_vertex(vertex_id)
        edge = Edge(edge_id, source, target, cost, delay, (start, end))
        self._edges[edge_id] = edge
        self._out[source].append(edge)

    @property
    def vertices(self) -> frozenset:
        """All vertex IDs, including isolated vertices."""
        return frozenset(self._vertices)

    @property
    def edges(self) -> Tuple[Edge, ...]:
        return tuple(self._edges.values())

    def has_vertex(self, vertex_id: str) -> bool:
        return vertex_id in self._vertices

    def out_edges(self, vertex_id: str) -> Tuple[Edge, ...]:
        return tuple(self._out.get(vertex_id, ()))

    def edge(self, edge_id: str) -> Edge:
        return self._edges[edge_id]


@dataclass(frozen=True)
class Path:
    """One Pareto-optimal simple path."""

    cost: float
    delay: float
    vertices: Tuple[str, ...]
    edges: Tuple[str, ...]


@dataclass(frozen=True)
class SearchResult:
    """Result of :func:`pareto_paths`.

    ``paths`` holds the Pareto frontier sorted by cost ascending (delay
    descending). ``truncated`` is True when ``max_results`` or
    ``max_labels`` cut the enumeration short; a truncated result is a
    partial, best-effort frontier and must not be reported as complete.
    """

    paths: Tuple[Path, ...]
    truncated: bool
    budget: float
    max_results: Optional[int]
    max_labels: int

    @property
    def is_complete(self) -> bool:
        """True only when the frontier is guaranteed complete."""
        return not self.truncated


def _dominates(a: Tuple[float, float], b: Tuple[float, float]) -> bool:
    """True iff point a Pareto-dominates point b (both dimensions)."""
    return a[0] <= b[0] and a[1] <= b[1] and (a[0] < b[0] or a[1] < b[1])


def _record(solutions: list, cost: float, delay: float,
            vertices: Tuple[str, ...], edges: Tuple[str, ...]) -> None:
    point = (cost, delay)
    for existing in solutions:
        if _dominates((existing[0], existing[1]), point):
            return
    solutions[:] = [
        existing for existing in solutions
        if not _dominates(point, (existing[0], existing[1]))
    ]
    solutions.append([cost, delay, vertices, edges])


def pareto_paths(
    graph: Graph,
    source: str,
    target: str,
    *,
    budget: float,
    max_results: Optional[int] = None,
    max_labels: int = 100_000,
) -> SearchResult:
    """Enumerate the Pareto frontier of feasible simple paths.

    Feasible means: simple (no repeated vertices), every edge entered
    inside its ``[start, end)`` window, and total ``cost <= budget``
    (boundary inclusive). Returns a :class:`SearchResult`; an empty
    ``paths`` tuple means no feasible path exists.
    """
    budget = float(budget)
    if budget < 0:
        raise ValueError("budget must be non-negative")
    if not graph.has_vertex(source):
        raise ValueError(f"unknown source vertex: {source!r}")
    if not graph.has_vertex(target):
        raise ValueError(f"unknown target vertex: {target!r}")
    if max_results is not None and max_results < 1:
        raise ValueError("max_results must be a positive integer or None")
    if max_labels < 0:
        raise ValueError("max_labels must be non-negative")

    solutions: list = []
    truncated = False

    if source == target:
        solutions.append([0.0, 0.0, (source,), ()])
    else:
        labels = 0
        # Iterative DFS. Each frame: (vertex, cost, delay, visited,
        # vertex_path, edge_path, out-edge iterator).
        stack = [(
            source, 0.0, 0.0, frozenset({source}), (source,), (),
            iter(graph.out_edges(source)),
        )]
        while stack and not truncated:
            vertex, cost, delay, visited, vpath, epath, edge_iter = stack[-1]
            edge = next(edge_iter, None)
            if edge is None:
                stack.pop()
                continue
            if edge.target in visited:
                continue  # simple paths only: no repeated vertices
            start, end = edge.window
            if not (start <= delay < end):
                continue  # outside [start, end): left closed, right open
            new_cost = cost + edge.cost
            new_delay = delay + edge.delay
            if new_cost > budget:
                continue  # budget boundary is inclusive
            if any(_dominates((s[0], s[1]), (new_cost, new_delay))
                   for s in solutions):
                continue  # any completion stays dominated (weights >= 0)
            labels += 1
            if labels > max_labels:
                truncated = True  # best-effort partial result, NOT complete
                break
            new_vpath = vpath + (edge.target,)
            new_epath = epath + (edge.id,)
            if edge.target == target:
                _record(solutions, new_cost, new_delay, new_vpath, new_epath)
                continue
            stack.append((
                edge.target, new_cost, new_delay,
                visited | {edge.target}, new_vpath, new_epath,
                iter(graph.out_edges(edge.target)),
            ))

    frontier = sorted(solutions, key=lambda s: (s[0], -s[1]))
    if max_results is not None and len(frontier) > max_results:
        frontier = frontier[:max_results]
        truncated = True  # capped output is partial, NOT complete

    paths = tuple(
        Path(cost=s[0], delay=s[1], vertices=s[2], edges=s[3])
        for s in frontier
    )
    return SearchResult(
        paths=paths,
        truncated=truncated,
        budget=budget,
        max_results=max_results,
        max_labels=max_labels,
    )
