"""Graph model: string vertex/edge IDs, parallel edges, isolated vertices.

Edges carry a non-negative length and a state-transition table mapping an
input state to the output state produced by traversing the edge.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Mapping


@dataclass(frozen=True)
class Edge:
    edge_id: str
    source: str
    target: str
    length: float
    transitions: Mapping[str, str]

    def __post_init__(self) -> None:
        if self.length < 0:
            raise ValueError(f"edge {self.edge_id!r} has negative length {self.length}")
        if not self.transitions:
            raise ValueError(f"edge {self.edge_id!r} allows no state transition")
        object.__setattr__(self, "transitions", dict(self.transitions))

    def next_state(self, state: str) -> str | None:
        """State produced by traversing this edge in ``state`` (None if not allowed)."""
        return self.transitions.get(state)


class Graph:
    """Directed multigraph with string IDs.

    Keeps isolated vertices and parallel edges. Outgoing edges are iterated
    in edge-ID order so expansion is deterministic.
    """

    def __init__(self) -> None:
        self._vertices: dict[str, None] = {}
        self._edges: dict[str, Edge] = {}
        self._outgoing: dict[str, list[str]] = {}

    def add_vertex(self, vertex_id: str) -> None:
        self._vertices.setdefault(vertex_id)
        self._outgoing.setdefault(vertex_id, [])

    def add_edge(self, edge: Edge) -> None:
        if edge.edge_id in self._edges:
            raise ValueError(f"duplicate edge id {edge.edge_id!r}")
        self.add_vertex(edge.source)
        self.add_vertex(edge.target)
        self._edges[edge.edge_id] = edge
        self._outgoing[edge.source].append(edge.edge_id)
        self._outgoing[edge.source].sort()

    def has_vertex(self, vertex_id: str) -> bool:
        return vertex_id in self._vertices

    @property
    def vertices(self) -> tuple[str, ...]:
        return tuple(self._vertices)

    @property
    def edges(self) -> tuple[Edge, ...]:
        return tuple(self._edges.values())

    def edge(self, edge_id: str) -> Edge:
        return self._edges[edge_id]

    def outgoing(self, vertex_id: str) -> tuple[Edge, ...]:
        return tuple(self._edges[eid] for eid in self._outgoing.get(vertex_id, ()))
