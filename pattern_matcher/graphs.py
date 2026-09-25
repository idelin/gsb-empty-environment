"""Graph data model.

Target graph: string vertex/edge ids, optional types, arbitrary property
dicts, multi-edges and isolated vertices are preserved. Edges may carry a
validity interval [valid_from, valid_to) -- left-closed, right-open.

Pattern graph: typed vertices with property predicates, directed edges with
optional type/property predicates and a `negative` flag (edge must NOT exist).
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Iterable, Mapping


# ---------------------------------------------------------------------------
# Property predicates
# ---------------------------------------------------------------------------

def match_predicates(preds: Mapping[str, tuple], props: Mapping[str, Any]) -> bool:
    """Evaluate property predicates against a property mapping.

    Each predicate is ``key -> (op, *args)`` with op in:
    eq, ne, lt, le, gt, ge, in, exists, missing.
    """
    for key, spec in preds.items():
        op = spec[0]
        args = spec[1:]
        present = key in props
        value = props.get(key)
        if op == "exists":
            if not present:
                return False
            continue
        if op == "missing":
            if present:
                return False
            continue
        if not present:
            return False
        if op == "eq":
            if value != args[0]:
                return False
        elif op == "ne":
            if value == args[0]:
                return False
        elif op == "lt":
            if not value < args[0]:
                return False
        elif op == "le":
            if not value <= args[0]:
                return False
        elif op == "gt":
            if not value > args[0]:
                return False
        elif op == "ge":
            if not value >= args[0]:
                return False
        elif op == "in":
            if value not in args[0]:
                return False
        else:
            raise ValueError(f"unknown predicate op: {op!r}")
    return True


# ---------------------------------------------------------------------------
# Target graph
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Vertex:
    id: str
    type: str | None = None
    props: Mapping[str, Any] = field(default_factory=dict)


@dataclass(frozen=True)
class Edge:
    id: str
    src: str
    dst: str
    type: str | None = None
    props: Mapping[str, Any] = field(default_factory=dict)
    valid_from: float | None = None
    valid_to: float | None = None

    def active_at(self, t: float) -> bool:
        """Interval is left-closed, right-open: [valid_from, valid_to)."""
        if self.valid_from is not None and t < self.valid_from:
            return False
        if self.valid_to is not None and t >= self.valid_to:
            return False
        return True


class TargetGraph:
    """Mutable target graph. `version` bumps on every mutating batch."""

    def __init__(self) -> None:
        self.vertices: dict[str, Vertex] = {}
        self.edges: dict[str, Edge] = {}
        self._out: dict[str, list[str]] = {}
        self._in: dict[str, list[str]] = {}
        self.version = 0

    # -- vertices ----------------------------------------------------------
    def add_vertex(self, vertex: Vertex) -> None:
        if vertex.id in self.vertices:
            raise ValueError(f"duplicate vertex id: {vertex.id!r}")
        self.vertices[vertex.id] = vertex
        self._out[vertex.id] = []
        self._in[vertex.id] = []

    def add_vertices(self, vertices: Iterable[Vertex]) -> None:
        for v in vertices:
            self.add_vertex(v)

    # -- edges (batch) -----------------------------------------------------
    def add_edges(self, edges: Iterable[Edge]) -> list[Edge]:
        edges = list(edges)
        for e in edges:
            if e.id in self.edges:
                raise ValueError(f"duplicate edge id: {e.id!r}")
            if e.src not in self.vertices or e.dst not in self.vertices:
                raise ValueError(f"edge {e.id!r} references unknown vertex")
        for e in edges:
            self.edges[e.id] = e
            self._out[e.src].append(e.id)
            self._in[e.dst].append(e.id)
        if edges:
            self.version += 1
        return edges

    def remove_edges(self, edge_ids: Iterable[str]) -> list[Edge]:
        edge_ids = list(edge_ids)
        removed = []
        for eid in edge_ids:
            if eid not in self.edges:
                raise KeyError(f"unknown edge id: {eid!r}")
            removed.append(self.edges[eid])
        for e in removed:
            del self.edges[e.id]
            self._out[e.src].remove(e.id)
            self._in[e.dst].remove(e.id)
        if removed:
            self.version += 1
        return removed

    # -- queries -----------------------------------------------------------
    def out_edges(self, vid: str, *, at_time: float | None = None) -> list[Edge]:
        return self._filter([self.edges[i] for i in self._out[vid]], at_time)

    def in_edges(self, vid: str, *, at_time: float | None = None) -> list[Edge]:
        return self._filter([self.edges[i] for i in self._in[vid]], at_time)

    def edges_between(self, src: str, dst: str, *,
                      at_time: float | None = None) -> list[Edge]:
        """All (possibly parallel) edges src -> dst, sorted by edge id."""
        found = [self.edges[eid] for eid in self._out.get(src, ())
                 if self.edges[eid].dst == dst]
        found = self._filter(found, at_time)
        found.sort(key=lambda e: e.id)
        return found

    def _filter(self, edges, at_time):
        if at_time is not None:
            edges = [e for e in edges if e.active_at(at_time)]
        return edges


# ---------------------------------------------------------------------------
# Pattern graph
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class PatternVertex:
    id: str
    type: str | None = None
    preds: Mapping[str, tuple] = field(default_factory=dict)


@dataclass(frozen=True)
class PatternEdge:
    id: str
    src: str
    dst: str
    type: str | None = None
    negative: bool = False
    preds: Mapping[str, tuple] = field(default_factory=dict)


class PatternGraph:
    def __init__(self, vertices: Iterable[PatternVertex],
                 edges: Iterable[PatternEdge]) -> None:
        vertices = list(vertices)
        self.vertices: dict[str, PatternVertex] = {v.id: v for v in vertices}
        if len(self.vertices) != len(vertices):
            raise ValueError("duplicate pattern vertex id")
        self.edges: list[PatternEdge] = list(edges)
        seen = set()
        for e in self.edges:
            if e.id in seen:
                raise ValueError(f"duplicate pattern edge id: {e.id!r}")
            seen.add(e.id)
            if e.src not in self.vertices or e.dst not in self.vertices:
                raise ValueError(f"pattern edge {e.id!r} references unknown vertex")
        self.positive_edges = [e for e in self.edges if not e.negative]
        self.negative_edges = [e for e in self.edges if e.negative]
