"""Graph model with string vertex/edge IDs, isolated vertices and multiedges."""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Edge:
    id: str
    u: str
    v: str

    def other(self, vertex: str) -> str:
        return self.v if self.u == vertex else self.u


class Graph:
    """Undirected graph. Keeps isolated vertices and parallel (multi) edges."""

    def __init__(self) -> None:
        self._vertices: dict[str, bool] = {}
        self._edges: list[Edge] = []
        self._adj: dict[str, list[Edge]] = {}

    def add_vertex(self, vid: str) -> None:
        if vid not in self._vertices:
            self._vertices[vid] = True
            self._adj[vid] = []

    def add_edge(self, eid: str, u: str, v: str) -> None:
        self.add_vertex(u)
        self.add_vertex(v)
        edge = Edge(eid, u, v)
        self._edges.append(edge)
        self._adj[u].append(edge)
        if v != u:
            self._adj[v].append(edge)

    @property
    def vertices(self) -> list[str]:
        return list(self._vertices)

    @property
    def edges(self) -> list[Edge]:
        return list(self._edges)

    def incident(self, vid: str) -> list[Edge]:
        return list(self._adj.get(vid, []))

    def without_edge(self, eid: str) -> "Graph":
        """Copy of this graph with every edge whose id is `eid` removed.

        Vertices (including isolated ones) are preserved.
        """
        g = Graph()
        for v in self.vertices:
            g.add_vertex(v)
        for e in self._edges:
            if e.id != eid:
                g.add_edge(e.id, e.u, e.v)
        return g
