"""立即支配者（immediate dominator）计算与单边变更后的受影响区域重算。

特性：
- 顶点、边均使用字符串 ID；保留孤立点与多重边。
- 边带有左闭右开的时间区间 [start, end)：在时刻 t 生效当且仅当 start <= t < end。
- 从入口用 Cooper-Harvey-Kennedy 迭代算法计算所有可达点的立即支配者，
  不把 DFS 父节点当作支配者；不可达点单独表示，不进入支配树。
- 支持支配查询与路径反例（一条从入口到目标、绕过指定点的路径）。
- 支持单边变更（加边 / 删边 / 改时间区间）后重算并报告受影响区域。

仅使用 Python 标准库，兼容 Python 3.11。
"""

from __future__ import annotations

import math
from collections import deque
from dataclasses import dataclass, field, replace


# ---------------------------------------------------------------------------
# 图结构
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Edge:
    """有向边。时间区间左闭右开：[start, end)。"""

    id: str
    src: str
    dst: str
    start: float = -math.inf  # 闭端：t >= start 时生效
    end: float = math.inf     # 开端：t >= end 时失效

    def active_at(self, t: float) -> bool:
        return self.start <= t < self.end


class Graph:
    """字符串 ID 的有向图。保留孤立点；允许同一对顶点间的多重边（边 ID 唯一）。"""

    def __init__(self) -> None:
        self.vertices: dict[str, None] = {}  # dict 保序，孤立点也会被保留
        self.edges: dict[str, Edge] = {}

    def add_vertex(self, vid: str) -> None:
        self.vertices.setdefault(vid, None)

    def add_edge(self, edge: Edge) -> None:
        if edge.id in self.edges:
            raise ValueError(f"duplicate edge id: {edge.id!r}")
        self.edges[edge.id] = edge
        self.add_vertex(edge.src)
        self.add_vertex(edge.dst)

    def remove_edge(self, edge_id: str) -> Edge:
        return self.edges.pop(edge_id)

    def set_edge_interval(self, edge_id: str, start: float, end: float) -> None:
        self.edges[edge_id] = replace(self.edges[edge_id], start=start, end=end)

    def active_edges(self, t: float) -> list[Edge]:
        return [e for e in self.edges.values() if e.active_at(t)]


# ---------------------------------------------------------------------------
# 快照：某一时刻的支配信息
# ---------------------------------------------------------------------------

@dataclass
class Snapshot:
    """某一时刻的计算结果。不可达点单独放在 unreachable，不进支配树。"""

    time: float
    reachable: frozenset          # 从入口可达的顶点（含入口）
    unreachable: frozenset        # 不可达顶点（含孤立点、不可达环）
    idom: dict                    # 可达点 -> 立即支配者；入口的 idom 为 None
    children: dict                # 支配树：支配者 -> 被支配的孩子列表

    def dominates(self, a: str, b: str) -> bool:
        """a 是否支配 b。b 不可达时返回 False（不可达点单独表示）。"""
        if b not in self.reachable:
            return False
        cur = b
        while cur is not None:
            if cur == a:
                return True
            cur = self.idom[cur]
        return False


def _reverse_postorder(entry: str, adj: dict) -> list:
    """从入口做迭代 DFS，返回逆后序（RPO）。只用于遍历顺序，不用于支配结论。"""
    post: list = []
    visited = {entry}
    stack = [(entry, iter(adj[entry]))]
    while stack:
        node, it = stack[-1]
        descended = False
        for e in it:
            if e.dst not in visited:
                visited.add(e.dst)
                stack.append((e.dst, iter(adj[e.dst])))
                descended = True
                break
        if not descended:
            post.append(node)
            stack.pop()
    post.reverse()
    return post


def _compute_snapshot(graph: Graph, entry: str, t: float) -> Snapshot:
    active = graph.active_edges(t)
    adj: dict[str, list] = {v: [] for v in graph.vertices}
    for e in active:
        adj[e.src].append(e)

    if entry not in graph.vertices:
        raise KeyError(f"entry vertex not in graph: {entry!r}")

    rpo = _reverse_postorder(entry, adj)
    reachable = frozenset(rpo)
    unreachable = frozenset(v for v in graph.vertices if v not in reachable)

    # Cooper-Harvey-Kennedy 迭代算法
    rpo_num = {v: i for i, v in enumerate(rpo)}
    preds: dict[str, list] = {v: [] for v in rpo}
    for e in active:
        if e.src in rpo_num and e.dst in rpo_num:
            preds[e.dst].append(e.src)  # 多重边会产生重复前驱，不影响正确性

    idom: dict[str, str | None] = {entry: entry}

    def intersect(b1: str, b2: str) -> str:
        # 沿 idom 链上爬，直到两个 finger 相遇；支配者在 RPO 中编号更小
        while b1 != b2:
            while rpo_num[b1] > rpo_num[b2]:
                b1 = idom[b1]
            while rpo_num[b2] > rpo_num[b1]:
                b2 = idom[b2]
        return b1

    changed = True
    while changed:
        changed = False
        for b in rpo:
            if b == entry:
                continue
            new_idom = None
            for p in preds[b]:
                if p not in idom:  # 前驱尚未处理
                    continue
                new_idom = p if new_idom is None else intersect(p, new_idom)
            if new_idom is not None and idom.get(b) != new_idom:
                idom[b] = new_idom
                changed = True

    idom[entry] = None  # 入口没有支配者

    children: dict[str, list] = {v: [] for v in rpo}
    for v in rpo:
        if v != entry:
            children[idom[v]].append(v)

    return Snapshot(time=t, reachable=reachable, unreachable=unreachable,
                    idom=idom, children=children)


# ---------------------------------------------------------------------------
# 查询结果
# ---------------------------------------------------------------------------

@dataclass
class Path:
    """一条从入口出发的路径，顶点与边 ID 交替对应。"""

    vertices: list  # [entry, ..., target]
    edges: list     # 长度比 vertices 少 1

    def __str__(self) -> str:
        parts = [self.vertices[0]]
        for eid, v in zip(self.edges, self.vertices[1:]):
            parts.append(f"--{eid}-->")
            parts.append(v)
        return "".join(parts)


# 支配查询状态
DOMINATES = "dominates"
NOT_DOMINATES = "not_dominates"
TARGET_UNREACHABLE = "target_unreachable"
UNKNOWN_VERTEX = "unknown_vertex"


@dataclass
class QueryResult:
    status: str
    path: Path | None = None  # status 为 NOT_DOMINATES 时给出路径反例


@dataclass
class ChangeReport:
    """单边变更后在时刻 at 的受影响区域。"""

    at: float
    affected: frozenset            # idom 变化或可达性变化的顶点全集
    became_reachable: frozenset
    became_unreachable: frozenset
    idom_changed: dict             # 顶点 -> (旧 idom, 新 idom)


# ---------------------------------------------------------------------------
# 引擎
# ---------------------------------------------------------------------------

class DominatorEngine:
    """维护图 + 入口，按需计算快照，支持单边变更后的受影响区域重算。"""

    def __init__(self, graph: Graph, entry: str) -> None:
        self.graph = graph
        self.entry = entry
        self._cache: dict[float, Snapshot] = {}

    # -- 快照 ---------------------------------------------------------------

    def snapshot(self, t: float) -> Snapshot:
        if t not in self._cache:
            self._cache[t] = _compute_snapshot(self.graph, self.entry, t)
        return self._cache[t]

    def notify_changed(self) -> None:
        """直接改图后调用，使缓存失效。"""
        self._cache.clear()

    # -- 支配查询 -------------------------------------------------------------

    def dominates(self, a: str, b: str, t: float) -> bool:
        snap = self.snapshot(t)
        if a not in snap.reachable and a not in snap.unreachable:
            return False
        return snap.dominates(a, b)

    def counterexample(self, a: str, b: str, t: float) -> Path | None:
        """若 a 不支配 b，返回一条从入口到 b、绕过 a 的路径；否则返回 None。"""
        snap = self.snapshot(t)
        if b not in snap.reachable or snap.dominates(a, b):
            return None
        adj: dict[str, list] = {v: [] for v in self.graph.vertices}
        for e in self.graph.active_edges(t):
            adj[e.src].append(e)
        # BFS，绕开 a
        prev: dict[str, tuple] = {}
        seen = {self.entry}
        queue = deque([self.entry])
        while queue:
            u = queue.popleft()
            if u == b:
                break
            for e in adj[u]:
                if e.dst == a or e.dst in seen:
                    continue
                seen.add(e.dst)
                prev[e.dst] = (u, e.id)
                queue.append(e.dst)
        if b not in seen and b != self.entry:
            return None
        vertices, edges = [b], []
        cur = b
        while cur != self.entry:
            u, eid = prev[cur]
            vertices.append(u)
            edges.append(eid)
            cur = u
        vertices.reverse()
        edges.reverse()
        return Path(vertices=vertices, edges=edges)

    def explain(self, a: str, b: str, t: float) -> QueryResult:
        """支配查询的完整解释：支配 / 不支配（附路径反例）/ 目标不可达。"""
        snap = self.snapshot(t)
        if b not in snap.reachable and b not in snap.unreachable:
            return QueryResult(UNKNOWN_VERTEX)
        if b in snap.unreachable:
            return QueryResult(TARGET_UNREACHABLE)
        if snap.dominates(a, b):
            return QueryResult(DOMINATES)
        return QueryResult(NOT_DOMINATES, self.counterexample(a, b, t))

    # -- 单边变更与受影响区域重算 --------------------------------------------

    def _diff(self, before: Snapshot, after: Snapshot, at: float) -> ChangeReport:
        became_reachable = frozenset(after.reachable - before.reachable)
        became_unreachable = frozenset(before.reachable - after.reachable)
        idom_changed = {}
        common = (before.reachable & after.reachable) - {self.entry}
        for v in common:
            old, new = before.idom[v], after.idom[v]
            if old != new:
                idom_changed[v] = (old, new)
        affected = became_reachable | became_unreachable | frozenset(idom_changed)
        return ChangeReport(at=at, affected=affected,
                            became_reachable=became_reachable,
                            became_unreachable=became_unreachable,
                            idom_changed=idom_changed)

    def _apply(self, at: float, mutate) -> ChangeReport:
        before = self.snapshot(at)
        mutate()
        self._cache.clear()
        after = self.snapshot(at)
        return self._diff(before, after, at)

    def add_edge(self, edge: Edge, at: float) -> ChangeReport:
        return self._apply(at, lambda: self.graph.add_edge(edge))

    def remove_edge(self, edge_id: str, at: float) -> ChangeReport:
        return self._apply(at, lambda: self.graph.remove_edge(edge_id))

    def set_edge_interval(self, edge_id: str, start: float, end: float,
                          at: float) -> ChangeReport:
        return self._apply(at, lambda: self.graph.set_edge_interval(edge_id, start, end))
