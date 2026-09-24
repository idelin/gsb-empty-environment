"""Core data model: graphs with string IDs, problems, intervals.

Conventions:
- Vertices and edges carry string IDs. Isolated vertices and multi-edges
  (several edges between the same pair, each with its own ID) are preserved.
- Time intervals are left-closed, right-open: [start, end).
"""

from __future__ import annotations

from dataclasses import dataclass, field


@dataclass(frozen=True)
class Interval:
    """Left-closed, right-open interval [start, end)."""

    start: float
    end: float

    def __post_init__(self) -> None:
        if not self.start < self.end:
            raise ValueError(f"empty interval [{self.start}, {self.end})")

    def __contains__(self, t: float) -> bool:
        return self.start <= t < self.end

    def overlaps(self, other: "Interval") -> bool:
        return self.start < other.end and other.start < self.end


@dataclass(frozen=True)
class Edge:
    id: str
    u: str
    v: str


class Graph:
    """Undirected graph. Keeps isolated vertices and multi-edges."""

    def __init__(self) -> None:
        self._vertices: dict[str, None] = {}  # insertion-ordered set
        self._edges: list[Edge] = []
        self._edge_ids: set[str] = set()

    def add_vertex(self, vid: str) -> None:
        if not isinstance(vid, str) or not vid:
            raise ValueError("vertex id must be a non-empty string")
        self._vertices.setdefault(vid)

    def add_edge(self, eid: str, u: str, v: str) -> None:
        if not isinstance(eid, str) or not eid:
            raise ValueError("edge id must be a non-empty string")
        if eid in self._edge_ids:
            raise ValueError(f"duplicate edge id: {eid}")
        self.add_vertex(u)
        self.add_vertex(v)
        self._edges.append(Edge(eid, u, v))
        self._edge_ids.add(eid)

    @property
    def vertices(self) -> list[str]:
        return list(self._vertices)

    @property
    def edges(self) -> list[Edge]:
        return list(self._edges)

    def edge(self, eid: str) -> Edge:
        for e in self._edges:
            if e.id == eid:
                return e
        raise KeyError(eid)

    def adjacency(self) -> dict[str, list[tuple[str, str]]]:
        """vertex -> list of (edge_id, other_endpoint). Self-loops appear once."""
        adj: dict[str, list[tuple[str, str]]] = {v: [] for v in self._vertices}
        for e in self._edges:
            if e.u == e.v:
                adj[e.u].append((e.id, e.v))
            else:
                adj[e.u].append((e.id, e.v))
                adj[e.v].append((e.id, e.u))
        return adj


@dataclass
class Problem:
    """A coloring problem.

    colors: ordered tuple of color names (order defines lexicographic order).
    capacities: color -> max number of vertices that may use it; a color
        missing from the mapping is unlimited.
    precoloring: vertex id -> color, fixed before search.
    """

    graph: Graph
    colors: tuple[str, ...]
    capacities: dict[str, int] = field(default_factory=dict)
    precoloring: dict[str, str] = field(default_factory=dict)

    def validate(self) -> None:
        if not self.colors:
            raise ValueError("at least one color is required")
        if len(set(self.colors)) != len(self.colors):
            raise ValueError("duplicate colors")
        known = set(self.colors)
        for c, k in self.capacities.items():
            if c not in known:
                raise ValueError(f"capacity for unknown color: {c}")
            if k < 0:
                raise ValueError(f"negative capacity for color {c}")
        verts = set(self.graph.vertices)
        for v, c in self.precoloring.items():
            if v not in verts:
                raise ValueError(f"precoloring of unknown vertex: {v}")
            if c not in known:
                raise ValueError(f"precoloring with unknown color: {c}")

    def color_order(self) -> dict[str, int]:
        return {c: i for i, c in enumerate(self.colors)}


def color_symmetry_classes(problem: Problem) -> list[tuple[str, ...]]:
    """Groups of interchangeable colors.

    Two colors are interchangeable iff they have equal capacity and neither
    is pinned by the precoloring. Only non-trivial groups (size >= 2) are
    returned, each ordered by the problem's color order.
    """
    groups: dict[object, list[str]] = {}
    for c in problem.colors:
        key = problem.capacities.get(c)
        groups.setdefault(key, []).append(c)
    pinned = set(problem.precoloring.values())
    classes = []
    for g in groups.values():
        rest = [c for c in g if c not in pinned]
        if len(rest) > 1:
            classes.append(tuple(rest))
    return classes


def coloring_key(problem: Problem, coloring: dict[str, str]) -> tuple[int, ...]:
    """Lexicographic key of a coloring: vertices sorted by id, colors by order."""
    order = problem.color_order()
    return tuple(order[coloring[v]] for v in sorted(problem.graph.vertices))
