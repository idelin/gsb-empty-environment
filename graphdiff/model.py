"""Graph data model: string-ID vertices/edges, multiedges, isolated vertices,
half-open validity intervals [start, end)."""
from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any, Optional


class _Missing:
    _inst = None

    def __new__(cls):
        if cls._inst is None:
            cls._inst = super().__new__(cls)
        return cls._inst

    def __repr__(self):
        return "MISSING"

    def __eq__(self, other):
        return isinstance(other, _Missing)

    def __hash__(self):
        return hash(_Missing)


MISSING = _Missing()


def values_equal(x: Any, y: Any) -> bool:
    """JSON-semantics equality: 1 and True differ, because their canonical
    serializations differ. This keeps attribute comparisons consistent with
    the content fingerprints used for baseline verification."""
    if x is MISSING or y is MISSING:
        return x is y
    if type(x) is not type(y):
        return False
    return x == y


def attrs_equal(a: dict, b: dict) -> bool:
    if a.keys() != b.keys():
        return False
    return all(values_equal(a[k], b[k]) for k in a)


@dataclass(frozen=True)
class Interval:
    """Validity interval, left-closed right-open: start <= t < end.

    None means unbounded on that side.
    """

    start: Optional[float] = None
    end: Optional[float] = None

    def contains(self, t: float) -> bool:
        if self.start is not None and t < self.start:
            return False
        if self.end is not None and t >= self.end:
            return False
        return True

    def overlaps(self, other: "Interval") -> bool:
        if self.end is not None and other.start is not None and self.end <= other.start:
            return False
        if other.end is not None and self.start is not None and other.end <= self.start:
            return False
        return True

    def to_data(self) -> list:
        return [self.start, self.end]

    @staticmethod
    def from_data(data: list) -> "Interval":
        return Interval(data[0], data[1])


ALWAYS = Interval()


@dataclass(eq=False)
class Vertex:
    id: str
    attrs: dict[str, Any] = field(default_factory=dict)
    interval: Interval = ALWAYS

    def copy(self) -> "Vertex":
        return Vertex(self.id, dict(self.attrs), self.interval)

    def __eq__(self, other):
        if not isinstance(other, Vertex):
            return NotImplemented
        return (self.id == other.id and self.interval == other.interval
                and attrs_equal(self.attrs, other.attrs))


@dataclass(eq=False)
class Edge:
    id: str
    src: str
    dst: str
    attrs: dict[str, Any] = field(default_factory=dict)
    interval: Interval = ALWAYS

    def copy(self) -> "Edge":
        return Edge(self.id, self.src, self.dst, dict(self.attrs), self.interval)

    def __eq__(self, other):
        if not isinstance(other, Edge):
            return NotImplemented
        return (self.id == other.id and self.src == other.src
                and self.dst == other.dst and self.interval == other.interval
                and attrs_equal(self.attrs, other.attrs))


class GraphError(ValueError):
    pass


class Graph:
    """Directed multigraph. Vertices and edges are keyed by string IDs, so
    parallel edges (same endpoints, different IDs) and isolated vertices are
    first-class. Edge identity is the edge ID, not the endpoint pair."""

    def __init__(
        self,
        vertices: Optional[dict[str, Vertex]] = None,
        edges: Optional[dict[str, Edge]] = None,
    ):
        self.vertices: dict[str, Vertex] = dict(vertices or {})
        self.edges: dict[str, Edge] = dict(edges or {})

    # -- construction helpers ------------------------------------------------
    def add_vertex(self, vid: str, attrs: Optional[dict] = None,
                   interval: Interval = ALWAYS) -> "Graph":
        if vid in self.vertices:
            raise GraphError(f"duplicate vertex id {vid!r}")
        self.vertices[vid] = Vertex(vid, dict(attrs or {}), interval)
        return self

    def add_edge(self, eid: str, src: str, dst: str,
                 attrs: Optional[dict] = None,
                 interval: Interval = ALWAYS) -> "Graph":
        if eid in self.edges:
            raise GraphError(f"duplicate edge id {eid!r}")
        self.edges[eid] = Edge(eid, src, dst, dict(attrs or {}), interval)
        return self

    # -- semantics -----------------------------------------------------------
    def validate(self) -> None:
        for e in self.edges.values():
            if e.src not in self.vertices:
                raise GraphError(f"edge {e.id!r} has missing src {e.src!r}")
            if e.dst not in self.vertices:
                raise GraphError(f"edge {e.id!r} has missing dst {e.dst!r}")

    def at(self, t: float) -> "Graph":
        """Subgraph of entities valid at time t (half-open intervals)."""
        vs = {i: v.copy() for i, v in self.vertices.items() if v.interval.contains(t)}
        es = {
            i: e.copy()
            for i, e in self.edges.items()
            if e.interval.contains(t) and e.src in vs and e.dst in vs
        }
        return Graph(vs, es)

    def multiplicity(self, src: str, dst: str) -> int:
        return sum(1 for e in self.edges.values() if e.src == src and e.dst == dst)

    def copy(self) -> "Graph":
        return Graph(
            {i: v.copy() for i, v in self.vertices.items()},
            {i: e.copy() for i, e in self.edges.items()},
        )

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Graph):
            return NotImplemented
        return self.vertices == other.vertices and self.edges == other.edges

    def __repr__(self) -> str:
        return f"Graph(|V|={len(self.vertices)}, |E|={len(self.edges)})"

    # -- canonical serialization (used for fingerprints & digests) -----------
    def to_data(self) -> dict:
        return {
            "vertices": [
                {"id": v.id, "attrs": v.attrs, "interval": v.interval.to_data()}
                for v in sorted(self.vertices.values(), key=lambda v: v.id)
            ],
            "edges": [
                {
                    "id": e.id,
                    "src": e.src,
                    "dst": e.dst,
                    "attrs": e.attrs,
                    "interval": e.interval.to_data(),
                }
                for e in sorted(self.edges.values(), key=lambda e: e.id)
            ],
        }

    @staticmethod
    def from_data(data: dict) -> "Graph":
        g = Graph()
        for v in data["vertices"]:
            g.vertices[v["id"]] = Vertex(
                v["id"], dict(v["attrs"]), Interval.from_data(v["interval"])
            )
        for e in data["edges"]:
            g.edges[e["id"]] = Edge(
                e["id"], e["src"], e["dst"], dict(e["attrs"]),
                Interval.from_data(e["interval"]),
            )
        return g

    def canonical(self) -> str:
        return json.dumps(self.to_data(), sort_keys=True, separators=(",", ":"))

    @staticmethod
    def from_canonical(text: str) -> "Graph":
        return Graph.from_data(json.loads(text))
