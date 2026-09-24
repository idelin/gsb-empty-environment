from __future__ import annotations

from dataclasses import dataclass, field
from types import MappingProxyType
from typing import Any, Iterable, Mapping, NamedTuple


def _frozen_map(values: Mapping[Any, Any]) -> Mapping[Any, Any]:
    return MappingProxyType(dict(values))


def _require_str(value: Any, name: str) -> str:
    if not isinstance(value, str) or not value:
        raise TypeError(f"{name} must be a non-empty string")
    return value


def _require_int(value: Any, name: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise TypeError(f"{name} must be an integer")
    return value


def _require_priority(value: Any) -> int:
    return _require_int(value, "priority")


def _require_bound(value: Any, name: str) -> int:
    return _require_int(value, name)


@dataclass(frozen=True, slots=True)
class Constraint:
    """A difference constraint ``tail - head <= weight``."""

    edge_id: str
    tail: str
    head: str
    weight: int
    lower: int
    upper: int
    priority: int = 0

    def __post_init__(self) -> None:
        _require_str(self.edge_id, "edge_id")
        _require_str(self.tail, "tail")
        _require_str(self.head, "head")
        _require_int(self.weight, "weight")
        _require_bound(self.lower, "lower")
        _require_bound(self.upper, "upper")
        _require_priority(self.priority)
        if self.lower >= self.upper:
            raise ValueError("constraint interval must be non-empty and half-open")


@dataclass(frozen=True, slots=True)
class Removal:
    edge_id: str
    lower: int
    upper: int
    priority: int = 0

    def __post_init__(self) -> None:
        _require_str(self.edge_id, "edge_id")
        _require_bound(self.lower, "lower")
        _require_bound(self.upper, "upper")
        _require_priority(self.priority)
        if self.lower >= self.upper:
            raise ValueError("removal interval must be non-empty and half-open")


@dataclass(frozen=True, slots=True)
class CycleEdge:
    edge_id: str
    version: int
    tail: str
    head: str
    weight: int


@dataclass(frozen=True, slots=True)
class Cycle:
    edges: tuple[CycleEdge, ...]
    weight: int

    @property
    def key(self) -> tuple[tuple[str, int], ...]:
        return tuple((edge.edge_id, edge.version) for edge in self.edges)

    @property
    def original_ids(self) -> tuple[str, ...]:
        return tuple(edge.edge_id for edge in self.edges)


@dataclass(frozen=True, slots=True)
class Solution:
    feasible: bool
    potentials: tuple[tuple[str, int], ...] = ()
    cycle: Cycle | None = None


def _extract_cycle(
    source: Any,
    predecessor_vertex: dict[str, str | None],
    predecessor_edge: dict[str, CycleEdge],
) -> Cycle:
    seen: dict[str, int] = {}
    cursor = source
    order: list[str] = []
    while cursor is not None and cursor not in seen:
        seen[cursor] = len(order)
        order.append(cursor)
        cursor = predecessor_vertex[cursor]
    if cursor is None or cursor not in seen:
        raise RuntimeError("Bellman-Ford reported a cycle but no predecessor cycle exists")

    cycle_vertices = order[seen[cursor] :]
    cycle_vertices.append(cursor)
    edges: list[CycleEdge] = []
    for destination in reversed(cycle_vertices[1:]):
        edge = predecessor_edge.get(destination)
        if edge is None:
            raise RuntimeError("negative-cycle predecessor evidence is incomplete")
        edges.append(edge)
    weight = sum(edge.weight for edge in edges)
    return Cycle(tuple(edges), weight)


def run_bellman_ford(
    vertices: Iterable[str],
    constraints: Iterable[Constraint | tuple[Any, Constraint]],
    *,
    full_passes: bool = False,
) -> Solution:
    """Run Bellman-Ford from a zero super-source.

    Every vertex starts at potential zero, which is equivalent to adding an
    invisible zero-weight super-source. Disconnected components are therefore
    checked independently.
    """

    vertex_list = list(vertices)
    records: list[tuple[Any, Constraint]] = []
    for item in constraints:
        if isinstance(item, Constraint):
            records.append((None, item))
        else:
            records.append(item)
    records.sort(
        key=lambda item: (
            item[1].tail,
            item[1].head,
            item[1].edge_id,
            item[0] if item[0] is not None else -1,
        )
    )

    dist = {vertex: 0 for vertex in vertex_list}
    predecessor_vertex: dict[str, str | None] = {vertex: None for vertex in vertex_list}
    predecessor_edge: dict[str, CycleEdge] = {}
    changed: Any = None
    pass_count = len(vertex_list)

    for _ in range(pass_count):
        changed = None
        for version, edge in records:
            candidate = dist[edge.tail] + edge.weight
            if candidate < dist[edge.head]:
                dist[edge.head] = candidate
                predecessor_vertex[edge.head] = edge.tail
                if version is not None:
                    predecessor_edge[edge.head] = CycleEdge(
                        edge.edge_id,
                        version,
                        edge.tail,
                        edge.head,
                        edge.weight,
                    )
                changed = edge.head
        if changed is None and not full_passes:
            break

    if changed is None:
        return Solution(True, tuple(sorted(dist.items())))

    cycle = _extract_cycle(changed, predecessor_vertex, predecessor_edge)
    return Solution(False, (), cycle)


class _SolutionPool:
    def __init__(self) -> None:
        self._solutions: dict[tuple[tuple[str, int], ...], Solution] = {}
        self.graph_builds = 0

    def solve(self, active: Mapping[str, tuple[int, Constraint]]) -> Solution:
        key = tuple(sorted((edge_id, version) for edge_id, (version, _) in active.items()))
        solution = self._solutions.get(key)
        if solution is not None:
            return solution

        self.graph_builds += 1
        vertices = {
            vertex
            for _, constraint in active.values()
            for vertex in (constraint.tail, constraint.head)
        }
        solution = run_bellman_ford(
            vertices,
            ((version, constraint) for edge_id, (version, constraint) in active.items()),
        )
        self._solutions[key] = solution
        return solution


@dataclass(frozen=True, slots=True)
class PotentialPiece:
    lower: int
    upper: int
    potentials: tuple[tuple[str, int], ...]

    def potential_map(self) -> dict[str, int]:
        return dict(self.potentials)


@dataclass(frozen=True, slots=True)
class RelaxationDecision:
    sequence: int
    interval: tuple[int, int]
    edge_id: str
    version: int
    priority: int
    removed_cycle: Cycle


@dataclass(frozen=True, slots=True)
class SegmentPiece:
    lower: int
    upper: int
    raw_solution: Solution
    relaxed_solution: Solution | None
    decisions: tuple[RelaxationDecision, ...]

    @property
    def feasible(self) -> bool:
        return self.raw_solution.feasible

    def displayed_solution(self, *, soft: bool) -> Solution:
        if soft and self.relaxed_solution is not None:
            return self.relaxed_solution
        return self.raw_solution


@dataclass(frozen=True, slots=True)
class FeasibleSegment:
    lower: int
    upper: int
    pieces: tuple[PotentialPiece, ...]


@dataclass(frozen=True, slots=True)
class ConflictInterval:
    lower: int
    upper: int
    cycle: Cycle
    pieces: tuple[SegmentPiece, ...]


@dataclass(frozen=True, slots=True)
class RelaxationInterval:
    lower: int
    upper: int
    removed_edges: tuple[tuple[str, int], ...]
    decisions: tuple[RelaxationDecision, ...]
    pieces: tuple[PotentialPiece, ...]


@dataclass(frozen=True, slots=True)
class TemporalQuery:
    lower: int
    upper: int
    pieces: tuple[SegmentPiece, ...]
    feasible_segments: tuple[FeasibleSegment, ...]
    conflicts: tuple[ConflictInterval, ...]
    relaxed_segments: tuple[FeasibleSegment, ...]
    relaxations: tuple[RelaxationInterval, ...]

    @property
    def is_globally_feasible(self) -> bool:
        return not self.conflicts

