"""Directed multigraph with string vertex/edge IDs.

Edges carry a non-negative length and a state transition
(``state_in`` -> ``state_out``).  Isolated vertices and parallel
edges are preserved.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Edge:
    eid: str
    src: str
    dst: str
    length: float
    state_in: str
    state_out: str


class Graph:
    def __init__(self) -> None:
        self._vertices: set[str] = set()
        self._edges: dict[str, Edge] = {}
        self._out: dict[str, list[Edge]] = {}

    def add_vertex(self, vid: str) -> None:
        if not isinstance(vid, str):
            raise TypeError("vertex id must be a string")
        self._vertices.add(vid)
        self._out.setdefault(vid, [])

    def add_edge(
        self,
        eid: str,
        src: str,
        dst: str,
        length: float,
        state_in: str,
        state_out: str,
    ) -> Edge:
        if not isinstance(eid, str):
            raise TypeError("edge id must be a string")
        if eid in self._edges:
            raise ValueError(f"duplicate edge id: {eid!r}")
        if length < 0:
            raise ValueError("edge length must be non-negative")
        edge = Edge(
            eid=eid,
            src=src,
            dst=dst,
            length=float(length),
            state_in=state_in,
            state_out=state_out,
        )
        self.add_vertex(src)
        self.add_vertex(dst)
        self._edges[eid] = edge
        self._out[src].append(edge)
        return edge

    @property
    def vertices(self) -> frozenset[str]:
        return frozenset(self._vertices)

    @property
    def edges(self) -> tuple[Edge, ...]:
        return tuple(self._edges.values())

    def has_vertex(self, vid: str) -> bool:
        return vid in self._vertices

    def edge(self, eid: str) -> Edge:
        return self._edges[eid]

    def out_edges(self, vid: str) -> tuple[Edge, ...]:
        return tuple(self._out.get(vid, ()))
