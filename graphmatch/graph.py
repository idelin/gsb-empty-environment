"""Target graph: string-ID vertices/edges, multi-edges, isolated vertices,
and left-closed right-open validity intervals on edges."""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional


@dataclass
class Vertex:
    id: str
    type: str = "vertex"
    attrs: Dict[str, Any] = field(default_factory=dict)


@dataclass
class Edge:
    id: str
    src: str
    dst: str
    type: str = "edge"
    attrs: Dict[str, Any] = field(default_factory=dict)
    valid_from: int = 0
    valid_to: Optional[int] = None  # None means +inf; interval is [valid_from, valid_to)

    def active_at(self, t: int) -> bool:
        return self.valid_from <= t and (self.valid_to is None or t < self.valid_to)


class TargetGraph:
    def __init__(self) -> None:
        self.vertices: Dict[str, Vertex] = {}
        self.edges: Dict[str, Edge] = {}
        self._out: Dict[str, List[str]] = {}
        self._in: Dict[str, List[str]] = {}

    def add_vertex(self, vertex: Vertex) -> None:
        if vertex.id in self.vertices:
            raise ValueError(f"duplicate vertex id: {vertex.id}")
        self.vertices[vertex.id] = vertex

    def add_edge(self, edge: Edge) -> None:
        if edge.id in self.edges:
            raise ValueError(f"duplicate edge id: {edge.id}")
        for endpoint in (edge.src, edge.dst):
            if endpoint not in self.vertices:
                raise ValueError(f"edge {edge.id} references missing vertex {endpoint}")
        self.edges[edge.id] = edge
        self._out.setdefault(edge.src, []).append(edge.id)
        self._in.setdefault(edge.dst, []).append(edge.id)

    def remove_edge(self, edge_id: str) -> Edge:
        edge = self.edges.pop(edge_id)
        self._out[edge.src].remove(edge_id)
        self._in[edge.dst].remove(edge_id)
        return edge

    def active_out(self, vertex_id: str, t: int) -> List[Edge]:
        return [self.edges[i] for i in self._out.get(vertex_id, ())
                if self.edges[i].active_at(t)]

    def active_in(self, vertex_id: str, t: int) -> List[Edge]:
        return [self.edges[i] for i in self._in.get(vertex_id, ())
                if self.edges[i].active_at(t)]

    def active_edges_between(self, u: str, v: str, t: int) -> List[Edge]:
        return [e for e in self.active_out(u, t) if e.dst == v]
