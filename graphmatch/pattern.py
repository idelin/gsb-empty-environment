"""Pattern graph: typed vertices with attribute predicates, positive
(existence) and negative (non-existence) edge constraints."""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Dict, Iterable, Optional, Tuple

_OPS = ("eq", "ne", "lt", "le", "gt", "ge", "exists")


@dataclass(frozen=True)
class AttrPred:
    key: str
    op: str
    value: Any = None

    def __post_init__(self) -> None:
        if self.op not in _OPS:
            raise ValueError(f"unknown predicate op: {self.op}")

    def test(self, attrs: Dict[str, Any]) -> bool:
        if self.op == "exists":
            return self.key in attrs
        if self.key not in attrs:
            return False
        v = attrs[self.key]
        if self.op == "eq":
            return v == self.value
        if self.op == "ne":
            return v != self.value
        if self.op == "lt":
            return v < self.value
        if self.op == "le":
            return v <= self.value
        if self.op == "gt":
            return v > self.value
        return v >= self.value  # ge


@dataclass(frozen=True)
class PatternVertex:
    id: str
    type: Optional[str] = None
    predicates: Tuple[AttrPred, ...] = ()


@dataclass(frozen=True)
class PatternEdge:
    id: str
    src: str
    dst: str
    type: Optional[str] = None
    exists: bool = True  # False => negative (non-existence) constraint
    predicates: Tuple[AttrPred, ...] = ()


class PatternGraph:
    def __init__(self, vertices: Iterable[PatternVertex],
                 edges: Iterable[PatternEdge] = ()) -> None:
        self.vertices: Dict[str, PatternVertex] = {}
        for v in vertices:
            if v.id in self.vertices:
                raise ValueError(f"duplicate pattern vertex id: {v.id}")
            self.vertices[v.id] = v
        self.edges = list(edges)
        self.edges_by_id: Dict[str, PatternEdge] = {}
        for e in self.edges:
            if e.id in self.edges_by_id:
                raise ValueError(f"duplicate pattern edge id: {e.id}")
            for endpoint in (e.src, e.dst):
                if endpoint not in self.vertices:
                    raise ValueError(
                        f"pattern edge {e.id} references missing vertex {endpoint}")
            self.edges_by_id[e.id] = e
