"""按加权距离把顶点分配给最近来源。

- 边权限定非负有理数（int 或 fractions.Fraction），精确计算。
- 顶点与边均使用字符串 ID，保留孤立点与多重边。
- 来源活动时间区间为左闭右开 [start, end)，end 为 None 表示无限。
- 支持并列归属：到多个来源的最短距离相等时，顶点归属这些来源的集合。
- 零权连通块内，并列来源完整传播到块内每个顶点。
- 来源停用后重算受影响区域，输出归属变化边界与距离证明。
"""

from __future__ import annotations

import heapq
from dataclasses import dataclass
from fractions import Fraction
from typing import Optional


def _rational(value, name):
    if isinstance(value, bool):
        raise TypeError(f"{name} 必须是有理数（int 或 Fraction），得到 bool")
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int):
        return Fraction(value)
    raise TypeError(f"{name} 必须是有理数（int 或 Fraction），得到 {type(value).__name__}")


@dataclass(frozen=True)
class Edge:
    edge_id: str
    u: str
    v: str
    weight: Fraction


@dataclass
class Source:
    source_id: str
    vertex_id: str
    start: Fraction
    end: Optional[Fraction]  # None 表示 +inf，区间为 [start, end)

    def active_at(self, moment: Fraction) -> bool:
        return self.start <= moment and (self.end is None or moment < self.end)


@dataclass(frozen=True)
class ProofStep:
    edge_id: str
    from_vertex: str
    to_vertex: str
    weight: Fraction
    cumulative: Fraction


@dataclass(frozen=True)
class DistanceProof:
    """从某来源顶点到目标顶点的最短路径证书，各步权重累计等于 total。"""

    source_vertex: str
    target_vertex: str
    total: Fraction
    steps: tuple


@dataclass(frozen=True)
class VertexAssignment:
    vertex_id: str
    distance: Optional[Fraction]  # None 表示不可达
    sources: frozenset
    proof: Optional[DistanceProof]


@dataclass(frozen=True)
class VertexChange:
    vertex_id: str
    old_distance: Optional[Fraction]
    old_sources: frozenset
    new_distance: Optional[Fraction]
    new_sources: frozenset
    proof: Optional[DistanceProof]  # 新距离的证明


class AssignmentSnapshot:
    """某一时刻全部顶点的归属快照。"""

    def __init__(self, time: Fraction, rows: dict):
        self.time = time
        self._rows = rows

    def __getitem__(self, vertex_id: str) -> VertexAssignment:
        return self._rows[vertex_id]

    def __iter__(self):
        return iter(self.rows())

    def rows(self) -> list:
        return [self._rows[v] for v in sorted(self._rows)]

    def owners_of(self, vertex_id: str) -> frozenset:
        return self._rows[vertex_id].sources

    def distance_of(self, vertex_id: str) -> Optional[Fraction]:
        return self._rows[vertex_id].distance


@dataclass
class ChangeReport:
    """来源停用后的重算报告。"""

    time: Fraction
    deactivated_source: str
    changes: dict          # vertex_id -> VertexChange，按顶点 ID 排序
    boundary_edges: list   # 一端归属变化、另一端不变的边 ID，按边 ID 排序
    before: AssignmentSnapshot
    after: AssignmentSnapshot


class Graph:
    def __init__(self):
        self.vertices: set = set()
        self.edges: dict = {}
        self._adj: dict = {}
        self.sources: dict = {}

    def add_vertex(self, vertex_id: str) -> None:
        self.vertices.add(vertex_id)
        self._adj.setdefault(vertex_id, [])

    def add_edge(self, edge_id: str, u: str, v: str, weight) -> Edge:
        if edge_id in self.edges:
            raise ValueError(f"边 ID 重复: {edge_id!r}")
        w = _rational(weight, "边权")
        if w < 0:
            raise ValueError(f"边权必须为非负有理数，得到 {w}")
        self.add_vertex(u)
        self.add_vertex(v)
        edge = Edge(edge_id, u, v, w)
        self.edges[edge_id] = edge
        self._adj[u].append(edge)
        self._adj[v].append(edge)
        return edge

    def add_source(self, source_id: str, vertex_id: str, start=0, end=None) -> Source:
        if source_id in self.sources:
            raise ValueError(f"来源 ID 重复: {source_id!r}")
        if vertex_id not in self.vertices:
            raise KeyError(f"未知顶点: {vertex_id!r}")
        s = _rational(start, "区间起点")
        e = None if end is None else _rational(end, "区间终点")
        if e is not None and not s < e:
            raise ValueError(f"时间区间需满足 start < end，得到 [{s}, {e})")
        src = Source(source_id, vertex_id, s, e)
        self.sources[source_id] = src
        return src

    def _dijkstra(self, seeds: dict):
        """seeds: vertex_id -> 初始距离。返回 (dist, parent)。"""
        dist = {v: None for v in self.vertices}
        parent = {}
        heap = []
        for v, d in seeds.items():
            if dist[v] is None or d < dist[v]:
                dist[v] = d
        for v, d in dist.items():
            if d is not None:
                heapq.heappush(heap, (d, v))
        while heap:
            d, u = heapq.heappop(heap)
            if d != dist[u]:
                continue
            for edge in self._adj[u]:
                other = edge.v if edge.u == u else edge.u
                nd = d + edge.weight
                if dist[other] is None or nd < dist[other]:
                    dist[other] = nd
                    parent[other] = (u, edge.edge_id)
                    heapq.heappush(heap, (nd, other))
        return dist, parent

    def _zero_components(self):
        uf = {v: v for v in self.vertices}

        def find(x):
            while uf[x] != x:
                uf[x] = uf[uf[x]]
                x = uf[x]
            return x

        for edge in self.edges.values():
            if edge.weight == 0:
                ru, rv = find(edge.u), find(edge.v)
                if ru != rv:
                    uf[ru] = rv
        comps = {}
        for v in self.vertices:
            comps.setdefault(find(v), []).append(v)
        return comps.values()

    def _propagate_zero_components(self, owners: dict) -> None:
        """零权连通块内各顶点距离相等，并列来源取并集后完整传播。"""
        for comp in self._zero_components():
            united = set()
            for v in comp:
                united |= owners[v]
            for v in comp:
                owners[v] = set(united)

    def _build_proof(self, vertex_id: str, dist: dict, parent: dict) -> Optional[DistanceProof]:
        if dist[vertex_id] is None:
            return None
        steps = []
        cur = vertex_id
        while cur in parent:
            prev, edge_id = parent[cur]
            steps.append(ProofStep(edge_id, prev, cur, self.edges[edge_id].weight, Fraction(0)))
            cur = prev
        steps.reverse()
        cumulative = Fraction(0)
        ordered = []
        for step in steps:
            cumulative += step.weight
            ordered.append(ProofStep(step.edge_id, step.from_vertex, step.to_vertex,
                                     step.weight, cumulative))
        return DistanceProof(source_vertex=cur, target_vertex=vertex_id,
                             total=dist[vertex_id], steps=tuple(ordered))

    def assignments(self, time=0) -> AssignmentSnapshot:
        """计算给定时刻每个顶点的最近来源集合（含并列）与距离证明。"""
        moment = _rational(time, "时刻")
        active = [s for s in self.sources.values() if s.active_at(moment)]
        dist, parent = self._dijkstra({s.vertex_id: Fraction(0) for s in active})
        owners = {v: set() for v in self.vertices}
        for src in active:
            single, _ = self._dijkstra({src.vertex_id: Fraction(0)})
            for vid in self.vertices:
                if single[vid] is not None and single[vid] == dist[vid]:
                    owners[vid].add(src.source_id)
        self._propagate_zero_components(owners)
        rows = {}
        for vid in self.vertices:
            rows[vid] = VertexAssignment(
                vertex_id=vid,
                distance=dist[vid],
                sources=frozenset(owners[vid]),
                proof=self._build_proof(vid, dist, parent),
            )
        return AssignmentSnapshot(moment, rows)

    def deactivate_source(self, source_id: str, at) -> ChangeReport:
        """在时刻 at 停用来源（end := at），重算受影响区域并输出变化报告。"""
        if source_id not in self.sources:
            raise KeyError(f"未知来源: {source_id!r}")
        moment = _rational(at, "停用时刻")
        src = self.sources[source_id]
        if not src.active_at(moment):
            raise ValueError(f"来源 {source_id!r} 在时刻 {moment} 本就不活跃")
        before = self.assignments(moment)
        src.end = moment
        after = self.assignments(moment)
        changes = {}
        for vid in sorted(self.vertices):
            old, new = before[vid], after[vid]
            if old.sources != new.sources or old.distance != new.distance:
                changes[vid] = VertexChange(
                    vertex_id=vid,
                    old_distance=old.distance,
                    old_sources=old.sources,
                    new_distance=new.distance,
                    new_sources=new.sources,
                    proof=new.proof,
                )
        boundary = []
        for edge_id in sorted(self.edges):
            edge = self.edges[edge_id]
            if (edge.u in changes) != (edge.v in changes):
                boundary.append(edge_id)
        return ChangeReport(moment, source_id, changes, boundary, before, after)


def _fmt_sources(sources: frozenset) -> str:
    return "{" + ", ".join(sorted(sources)) + "}" if sources else "{}"


def _fmt_dist(distance) -> str:
    return "不可达" if distance is None else str(distance)


def _fmt_proof(proof: Optional[DistanceProof]) -> str:
    if proof is None:
        return "无（不可达）"
    if not proof.steps:
        return f"{proof.source_vertex}（即来源所在顶点，距离 0）"
    hops = " ".join(
        f"—{s.edge_id}({s.weight})→ {s.to_vertex}" for s in proof.steps
    )
    return f"{proof.source_vertex} {hops}，累计 {proof.total}"


def main() -> None:
    print("=== 样例 1：链 A—X—B，两边等长 3/2，来源 srcA@A、srcB@B ===")
    g = Graph()
    g.add_edge("e_AX", "A", "X", Fraction(3, 2))
    g.add_edge("e_XB", "X", "B", Fraction(3, 2))
    g.add_source("srcA", "A")
    g.add_source("srcB", "B")

    snap = g.assignments(0)
    print("时刻 0 归属：")
    for row in snap:
        print(f"  {row.vertex_id}: 距离 {_fmt_dist(row.distance)}, 归属 {_fmt_sources(row.sources)}")

    report = g.deactivate_source("srcA", 10)
    print("\n时刻 10 停用 srcA，受影响区域：")
    for vid, change in report.changes.items():
        print(f"  {vid}: {_fmt_sources(change.old_sources)}@{_fmt_dist(change.old_distance)}"
              f" -> {_fmt_sources(change.new_sources)}@{_fmt_dist(change.new_distance)}")
    print("归属变化边界边：")
    for edge_id in report.boundary_edges:
        edge = g.edges[edge_id]
        state = {edge.u: "变化" if edge.u in report.changes else "不变",
                 edge.v: "变化" if edge.v in report.changes else "不变"}
        print(f"  {edge_id}: {edge.u}({state[edge.u]}) — {edge.v}({state[edge.v]})")
    print("新距离证明：")
    for vid, change in report.changes.items():
        print(f"  {vid}: {_fmt_proof(change.proof)}")

    print("\n=== 样例 2：零权连通块内并列来源完整传播 ===")
    g2 = Graph()
    g2.add_edge("e1", "A", "X", 1)
    g2.add_edge("e2", "B", "Y", 1)
    g2.add_edge("e0", "X", "Y", 0)
    g2.add_source("sA", "A")
    g2.add_source("sB", "B")
    snap2 = g2.assignments(0)
    for row in snap2:
        print(f"  {row.vertex_id}: 距离 {_fmt_dist(row.distance)}, 归属 {_fmt_sources(row.sources)}")


if __name__ == "__main__":
    main()
