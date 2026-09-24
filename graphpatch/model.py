"""Core graph model: vertices/edges with string IDs, attributes, and
left-closed right-open time intervals [start, end).

Only the Python standard library is used.
"""
from __future__ import annotations

import copy
import hashlib
import json
import zlib
from dataclasses import dataclass, field
from typing import Any, Callable, Iterator, Optional

INF = float("inf")
NEG_INF = float("-inf")


@dataclass(frozen=True)
class Interval:
    """A left-closed, right-open time interval [start, end)."""

    start: float = NEG_INF
    end: float = INF

    def __post_init__(self) -> None:
        if not (self.start < self.end):
            raise ValueError(
                f"invalid interval [{self.start}, {self.end}): need start < end"
            )

    def contains(self, t: float) -> bool:
        """Left-closed right-open membership: start <= t < end."""
        return self.start <= t < self.end

    def overlaps(self, other: "Interval") -> bool:
        """Half-open overlap: [0, 1) and [1, 2) do NOT overlap."""
        return self.start < other.end and other.start < self.end

    def to_list(self) -> list:
        start = None if self.start == NEG_INF else self.start
        end = None if self.end == INF else self.end
        return [start, end]

    @staticmethod
    def from_list(pair: list) -> "Interval":
        start, end = pair
        return Interval(
            NEG_INF if start is None else float(start),
            INF if end is None else float(end),
        )


ALWAYS = Interval()


@dataclass(frozen=True)
class Vertex:
    vid: str
    attrs: dict = field(default_factory=dict)
    interval: Interval = ALWAYS


@dataclass(frozen=True)
class Edge:
    eid: str
    src: str
    dst: str
    attrs: dict = field(default_factory=dict)
    interval: Interval = ALWAYS


class GraphError(ValueError):
    pass


class Graph:
    """A directed multigraph.

    - Vertices and edges carry string IDs.
    - Parallel edges (same endpoints, different edge IDs) are allowed and
      their multiplicity is significant.
    - Isolated vertices are first-class citizens.
    """

    def __init__(self) -> None:
        self.vertices: dict[str, Vertex] = {}
        self.edges: dict[str, Edge] = {}

    # -- construction helpers (mutating) ---------------------------------

    def add_vertex(self, vertex: Vertex) -> None:
        if vertex.vid in self.vertices:
            raise GraphError(f"duplicate vertex id {vertex.vid!r}")
        self.vertices[vertex.vid] = vertex

    def remove_vertex(self, vid: str) -> Vertex:
        if vid not in self.vertices:
            raise GraphError(f"no such vertex {vid!r}")
        for edge in self.edges.values():
            if edge.src == vid or edge.dst == vid:
                raise GraphError(
                    f"vertex {vid!r} still has incident edge {edge.eid!r}"
                )
        return self.vertices.pop(vid)

    def add_edge(self, edge: Edge) -> None:
        if edge.eid in self.edges:
            raise GraphError(f"duplicate edge id {edge.eid!r}")
        for endpoint in (edge.src, edge.dst):
            if endpoint not in self.vertices:
                raise GraphError(
                    f"edge {edge.eid!r} references missing vertex {endpoint!r}"
                )
        self.edges[edge.eid] = edge

    def remove_edge(self, eid: str) -> Edge:
        if eid not in self.edges:
            raise GraphError(f"no such edge {eid!r}")
        return self.edges.pop(eid)

    # -- derived views ----------------------------------------------------

    def copy(self) -> "Graph":
        g = Graph()
        g.vertices = copy.deepcopy(self.vertices)
        g.edges = copy.deepcopy(self.edges)
        return g

    def active_at(self, t: float) -> "Graph":
        """Subgraph of vertices/edges whose [start, end) interval contains t."""
        g = Graph()
        for v in self.vertices.values():
            if v.interval.contains(t):
                g.vertices[v.vid] = copy.deepcopy(v)
        for e in self.edges.values():
            if (
                e.interval.contains(t)
                and e.src in g.vertices
                and e.dst in g.vertices
            ):
                g.edges[e.eid] = copy.deepcopy(e)
        return g

    def edge_multiplicity(self, src: str, dst: str) -> int:
        return sum(
            1 for e in self.edges.values() if e.src == src and e.dst == dst
        )

    # -- canonical form, digests, equality --------------------------------

    def canonical(self) -> str:
        """Deterministic canonical serialization of the full graph content."""
        payload = {
            "vertices": [
                {
                    "id": v.vid,
                    "attrs": v.attrs,
                    "interval": v.interval.to_list(),
                }
                for v in sorted(self.vertices.values(), key=lambda v: v.vid)
            ],
            "edges": [
                {
                    "id": e.eid,
                    "src": e.src,
                    "dst": e.dst,
                    "attrs": e.attrs,
                    "interval": e.interval.to_list(),
                }
                for e in sorted(self.edges.values(), key=lambda e: e.eid)
            ],
        }
        return json.dumps(payload, sort_keys=True, separators=(",", ":"))

    def content_hash(self) -> str:
        """Strong content fingerprint (sha256 of the canonical form)."""
        return hashlib.sha256(self.canonical().encode("utf-8")).hexdigest()

    def weak_digest(self) -> str:
        """Fast, collision-prone digest (crc32). Never trust it alone."""
        return crc32_digest(self.canonical())

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Graph):
            return NotImplemented
        return self.canonical() == other.canonical()

    def __repr__(self) -> str:
        return f"Graph(vertices={len(self.vertices)}, edges={len(self.edges)})"


def crc32_digest(text: str) -> str:
    return format(zlib.crc32(text.encode("utf-8")), "08x")


# The default weak digest used for the fast-path base check.  Callers may
# inject a different (even weaker) function, e.g. to study collisions.
DigestFn = Callable[[str], str]
DEFAULT_DIGEST_FN: DigestFn = crc32_digest
