"""Priority-based path capacity reservation with failure rerouting.

Conventions:
- Vertex and edge IDs are strings. Isolated vertices and parallel
  (multi-)edges are preserved.
- Time intervals are half-open: [start, end).
- Higher priority value reserves first; ties are broken by demand ID
  (ascending string order).
- Edges are undirected.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass


@dataclass(frozen=True)
class Interval:
    start: float
    end: float

    def __post_init__(self) -> None:
        if not self.start < self.end:
            raise ValueError(f"invalid interval [{self.start}, {self.end})")

    def overlaps(self, other: "Interval") -> bool:
        return self.start < other.end and other.start < self.end


@dataclass
class Edge:
    id: str
    u: str
    v: str
    capacity: float
    failed: bool = False


@dataclass
class Demand:
    id: str
    priority: int
    source: str
    target: str
    bandwidth: float
    interval: Interval


@dataclass(frozen=True)
class Reservation:
    demand_id: str
    interval: Interval
    bandwidth: float


class Network:
    def __init__(self) -> None:
        self.vertices: dict[str, None] = {}
        self.edges: dict[str, Edge] = {}
        self._adj: dict[str, list[str]] = {}
        self._reservations: dict[str, list[Reservation]] = {}
        self._placements: dict[str, list[str]] = {}

    def add_vertex(self, vertex_id: str) -> None:
        self.vertices.setdefault(vertex_id, None)
        self._adj.setdefault(vertex_id, [])

    def add_edge(self, edge_id: str, u: str, v: str, capacity: float) -> None:
        if edge_id in self.edges:
            raise ValueError(f"duplicate edge id: {edge_id}")
        if capacity <= 0:
            raise ValueError("capacity must be positive")
        self.add_vertex(u)
        self.add_vertex(v)
        self.edges[edge_id] = Edge(edge_id, u, v, capacity)
        self._adj[u].append(edge_id)
        self._adj[v].append(edge_id)
        self._reservations[edge_id] = []

    def _max_load(self, edge_id: str, interval: Interval) -> float:
        points = {interval.start, interval.end}
        overlapping = []
        for r in self._reservations[edge_id]:
            if r.interval.overlaps(interval):
                overlapping.append(r)
                points.add(max(r.interval.start, interval.start))
                points.add(min(r.interval.end, interval.end))
        ordered = sorted(points)
        load = 0.0
        for a, b in zip(ordered, ordered[1:]):
            if a >= b:
                continue
            seg = sum(
                r.bandwidth
                for r in overlapping
                if r.interval.start < b and r.interval.end > a
            )
            load = max(load, seg)
        return load

    def available_capacity(self, edge_id: str, interval: Interval) -> float:
        return self.edges[edge_id].capacity - self._max_load(edge_id, interval)

    def find_path(
        self, source: str, target: str, bandwidth: float, interval: Interval
    ) -> list[str] | None:
        if source not in self.vertices or target not in self.vertices:
            return None
        if source == target:
            return []
        prev: dict[str, tuple[str, str] | None] = {source: None}
        queue: deque[str] = deque([source])
        while queue:
            u = queue.popleft()
            if u == target:
                break
            for edge_id in self._adj[u]:
                edge = self.edges[edge_id]
                if edge.failed:
                    continue
                w = edge.v if edge.u == u else edge.u
                if w in prev:
                    continue
                if self.available_capacity(edge_id, interval) < bandwidth:
                    continue
                prev[w] = (u, edge_id)
                queue.append(w)
        if target not in prev:
            return None
        path: list[str] = []
        cur = target
        while cur != source:
            parent, edge_id = prev[cur]  # type: ignore[misc]
            path.append(edge_id)
            cur = parent
        path.reverse()
        return path

    def reserve_path(self, demand: Demand, path: list[str]) -> bool:
        """Acquire edges one by one; roll back partial acquisitions on failure."""
        acquired: list[str] = []
        for edge_id in path:
            edge = self.edges[edge_id]
            if edge.failed or (
                self.available_capacity(edge_id, demand.interval) < demand.bandwidth
            ):
                for done in acquired:
                    self._release_on_edge(demand.id, done)
                return False
            self._reservations[edge_id].append(
                Reservation(demand.id, demand.interval, demand.bandwidth)
            )
            acquired.append(edge_id)
        self._placements[demand.id] = list(path)
        return True

    def _release_on_edge(self, demand_id: str, edge_id: str) -> None:
        self._reservations[edge_id] = [
            r for r in self._reservations[edge_id] if r.demand_id != demand_id
        ]

    def release_demand(self, demand_id: str) -> None:
        path = self._placements.pop(demand_id, None)
        if path is None:
            return
        for edge_id in path:
            self._release_on_edge(demand_id, edge_id)

    def placement(self, demand_id: str) -> list[str] | None:
        path = self._placements.get(demand_id)
        return list(path) if path is not None else None

    def reserve_all(self, demands: list[Demand]) -> dict[str, list[str] | None]:
        """Reserve a fixed demand list: priority desc, then demand ID asc."""
        result: dict[str, list[str] | None] = {}
        for demand in sorted(demands, key=lambda d: (-d.priority, d.id)):
            path = self.find_path(
                demand.source, demand.target, demand.bandwidth, demand.interval
            )
            if path is not None and self.reserve_path(demand, path):
                result[demand.id] = path
            else:
                result[demand.id] = None
        return result

    def fail_edge(
        self, edge_id: str, demands: dict[str, Demand]
    ) -> dict[str, list[str] | None]:
        """Fail an edge and reroute only the demands that used it.

        Affected demands are released first, then rerouted in
        (-priority, id) order. Demands that cannot be rerouted stay
        unreserved (their old reservation is rolled back).
        """
        edge = self.edges[edge_id]
        if edge.failed:
            return {}
        edge.failed = True
        affected = [
            demand_id
            for demand_id, path in self._placements.items()
            if edge_id in path
        ]
        for demand_id in affected:
            self.release_demand(demand_id)
        result: dict[str, list[str] | None] = {}
        for demand_id in sorted(
            affected, key=lambda d: (-demands[d].priority, d)
        ):
            demand = demands[demand_id]
            path = self.find_path(
                demand.source, demand.target, demand.bandwidth, demand.interval
            )
            if path is not None and self.reserve_path(demand, path):
                result[demand_id] = path
            else:
                result[demand_id] = None
        return result

    def recover_edge(self, edge_id: str) -> bool:
        """Recover a failed edge. Idempotent: never adds capacity twice."""
        edge = self.edges[edge_id]
        if not edge.failed:
            return False
        edge.failed = False
        return True
