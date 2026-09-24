"""数据模型：时间区间、图（顶点/边）、需求与预留。

约定：
- 顶点与边均使用字符串 ID。
- 图保留孤立点，允许同一对顶点间的多重边（边以自身 ID 区分）。
- 时间区间为左闭右开 [start, end)。
"""

from __future__ import annotations

from dataclasses import dataclass, field


@dataclass(frozen=True)
class Interval:
    """左闭右开时间区间 [start, end)。"""

    start: float
    end: float

    def __post_init__(self) -> None:
        if not self.start < self.end:
            raise ValueError(f"非法区间 [{self.start}, {self.end})：要求 start < end")

    def overlaps(self, other: "Interval") -> bool:
        """左闭右开语义：首尾相接（a.end == b.start）不算重叠。"""
        return self.start < other.end and other.start < self.end


@dataclass
class Edge:
    edge_id: str
    endpoint_a: str
    endpoint_b: str
    capacity: float
    failed: bool = False

    def __post_init__(self) -> None:
        if self.capacity <= 0:
            raise ValueError(f"边 {self.edge_id!r} 容量必须为正数")

    def other_endpoint(self, vertex_id: str) -> str:
        if vertex_id == self.endpoint_a:
            return self.endpoint_b
        if vertex_id == self.endpoint_b:
            return self.endpoint_a
        raise ValueError(f"顶点 {vertex_id!r} 不在边 {self.edge_id!r} 上")


class Graph:
    """无向图：保留孤立点，允许多重边。"""

    def __init__(self) -> None:
        self.vertices: set[str] = set()
        self.edges: dict[str, Edge] = {}
        self._adjacency: dict[str, list[str]] = {}

    def add_vertex(self, vertex_id: str) -> None:
        self.vertices.add(vertex_id)
        self._adjacency.setdefault(vertex_id, [])

    def add_edge(self, edge_id: str, endpoint_a: str, endpoint_b: str, capacity: float) -> Edge:
        if edge_id in self.edges:
            raise ValueError(f"边 ID 重复：{edge_id!r}")
        self.add_vertex(endpoint_a)
        self.add_vertex(endpoint_b)
        edge = Edge(edge_id, endpoint_a, endpoint_b, capacity)
        self.edges[edge_id] = edge
        self._adjacency[endpoint_a].append(edge_id)
        self._adjacency[endpoint_b].append(edge_id)
        return edge

    def incident_edges(self, vertex_id: str) -> list[str]:
        if vertex_id not in self.vertices:
            raise KeyError(f"顶点不存在：{vertex_id!r}")
        return list(self._adjacency[vertex_id])

    def neighbors(self, vertex_id: str):
        """产出 (edge_id, 对端顶点ID)，多重边逐条出现。"""
        for edge_id in self.incident_edges(vertex_id):
            yield edge_id, self.edges[edge_id].other_endpoint(vertex_id)


@dataclass(frozen=True)
class Demand:
    demand_id: str
    source: str
    target: str
    bandwidth: float
    interval: Interval
    priority: int = 0  # 数值越大优先级越高

    def __post_init__(self) -> None:
        if self.bandwidth <= 0:
            raise ValueError(f"需求 {self.demand_id!r} 带宽必须为正数")


@dataclass
class Reservation:
    """一条已承诺的预留：需求 + 占用的边 ID 有序序列。"""

    demand: Demand
    path: tuple[str, ...] = field(default_factory=tuple)
