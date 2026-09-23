"""动态多重图：维护桥、割点（关节点）与双连通块。

特性：
- 顶点、边均使用字符串 ID；孤立点与多重边（平行边）都会被保留。
- 自环按多重图语义处理：自环永远不是桥，不影响割点判定，
  在双连通块中自环单独构成一个块。
- 每条边带有生效时间区间 [start, end)，左闭右开；
  查询在某个时刻 t 上进行，只考虑当时存活的边。
- 支持边的批量添加 / 删除（apply_batch），批量更新后重新全量计算，
  保证结果与从头运行全量算法完全一致。
"""
from __future__ import annotations

import math
from collections import defaultdict


class Edge:
    """一条带生命周期的边。start <= t < end 时边存活（左闭右开）。"""

    __slots__ = ("edge_id", "u", "v", "start", "end")

    def __init__(self, edge_id: str, u: str, v: str,
                 start: float = 0, end: float = math.inf):
        self.edge_id = edge_id
        self.u = u
        self.v = v
        self.start = start
        self.end = end

    def is_loop(self) -> bool:
        return self.u == self.v

    def active_at(self, t: float) -> bool:
        return self.start <= t < self.end

    def __repr__(self) -> str:  # pragma: no cover
        return f"Edge({self.edge_id!r}, {self.u!r}, {self.v!r}, [{self.start}, {self.end}))"


class Analysis:
    """某一时刻 t 的全量分析结果。"""

    def __init__(self, t, bridges, articulation_points, blocks, components):
        self.t = t
        self.bridges = bridges                      # set[str]：桥边 ID
        self.articulation_points = articulation_points  # set[str]：割点
        self.blocks = blocks                        # list[frozenset[str]]：双连通块（边集）
        self.components = components                # list[frozenset[str]]：连通分量（点集，含孤立点）


class DynamicMultigraph:
    """支持批量增删边、按时刻查询桥 / 割点 / 双连通块的动态多重图。"""

    def __init__(self):
        self._vertices: set[str] = set()
        self._edges: dict[str, Edge] = {}
        self._cache: dict[float, Analysis] = {}

    # ------------------------------------------------------------------ #
    # 结构变更
    # ------------------------------------------------------------------ #
    def add_vertex(self, v: str) -> None:
        """添加顶点（允许孤立点）。"""
        self._vertices.add(v)
        self._cache.clear()

    def add_edge(self, edge_id: str, u: str, v: str,
                 start: float = 0, end: float = math.inf) -> None:
        """添加边。端点不存在时自动补点；时间区间左闭右开 [start, end)。"""
        if edge_id in self._edges:
            raise ValueError(f"边 ID 重复: {edge_id!r}")
        if not start < end:
            raise ValueError(f"非法时间区间 [{start}, {end})")
        self._vertices.update((u, v))
        self._edges[edge_id] = Edge(edge_id, u, v, start, end)
        self._cache.clear()

    def remove_edge(self, edge_id: str) -> None:
        """删除边（顶点保留，可能因此成为孤立点）。"""
        if edge_id not in self._edges:
            raise KeyError(f"边不存在: {edge_id!r}")
        del self._edges[edge_id]
        self._cache.clear()

    def apply_batch(self, adds=(), removes=()) -> None:
        """批量更新：先执行 removes（边 ID 序列），再执行 adds。

        adds 中每项为 (edge_id, u, v) 或 (edge_id, u, v, start, end)。
        整批应用完后才重新计算，结果与全量算法一致。
        """
        for edge_id in removes:
            self.remove_edge(edge_id)
        for spec in adds:
            self.add_edge(*spec)

    # ------------------------------------------------------------------ #
    # 查询
    # ------------------------------------------------------------------ #
    @property
    def vertices(self) -> set[str]:
        return set(self._vertices)

    def active_edges(self, t: float = 0) -> list[Edge]:
        """时刻 t 存活的边（start <= t < end）。"""
        return [e for e in self._edges.values() if e.active_at(t)]

    def analyze(self, t: float = 0) -> Analysis:
        """时刻 t 的桥、割点、双连通块、连通分量（带缓存）。"""
        if t not in self._cache:
            self._cache[t] = self._compute(t)
        return self._cache[t]

    def bridges(self, t: float = 0) -> set[str]:
        return self.analyze(t).bridges

    def articulation_points(self, t: float = 0) -> set[str]:
        return self.analyze(t).articulation_points

    def biconnected_components(self, t: float = 0) -> list[frozenset]:
        return self.analyze(t).blocks

    def components(self, t: float = 0) -> list[frozenset]:
        return self.analyze(t).components

    def separated_sets(self, edge_id: str, t: float = 0):
        """若 edge_id 在时刻 t 失效会分开图，返回被分开的两个顶点集合。

        返回 (side_u, side_v)：分别为删除该边后含 u、含 v 的连通块。
        若边不存在于图中抛 KeyError；边在 t 时刻不存活或不是桥（失效
        不会分开任何顶点）时返回 None。
        """
        if edge_id not in self._edges:
            raise KeyError(f"边不存在: {edge_id!r}")
        edge = self._edges[edge_id]
        if not edge.active_at(t) or edge_id not in self.bridges(t):
            return None
        adj = self._adjacency(t, skip_edge=edge_id)
        side_u = self._bfs(edge.u, adj)
        side_v = self._bfs(edge.v, adj)
        return frozenset(side_u), frozenset(side_v)

    # ------------------------------------------------------------------ #
    # 内部实现
    # ------------------------------------------------------------------ #
    def _adjacency(self, t: float, skip_edge: str | None = None):
        """邻接表：顶点 -> [(邻居, 边ID)]。自环不参与连通性，跳过。"""
        adj = {v: [] for v in self._vertices}
        for e in self._edges.values():
            if e.edge_id == skip_edge or not e.active_at(t) or e.is_loop():
                continue
            adj[e.u].append((e.v, e.edge_id))
            adj[e.v].append((e.u, e.edge_id))
        for lst in adj.values():  # 排序保证结果确定性
            lst.sort()
        return adj

    @staticmethod
    def _bfs(start: str, adj) -> set[str]:
        seen = {start}
        stack = [start]
        while stack:
            for nb, _ in adj[stack.pop()]:
                if nb not in seen:
                    seen.add(nb)
                    stack.append(nb)
        return seen

    def _compute(self, t: float) -> Analysis:
        """迭代式 Tarjan 低链算法，一次 DFS 同时求桥、割点、双连通块。

        用边 ID 区分父边，平行边自然按回边处理（多重图语义）；
        自环不进邻接表，最后各自单独成块。
        """
        adj = self._adjacency(t)
        disc: dict[str, int] = {}
        low: dict[str, int] = {}
        parent_edge: dict[str, str] = {}
        children: dict[str, int] = defaultdict(int)
        bridges: set[str] = set()
        articulations: set[str] = set()
        blocks: list[frozenset] = []
        edge_stack: list[str] = []
        timer = 0

        for root in sorted(self._vertices):
            if root in disc:
                continue
            disc[root] = low[root] = timer
            timer += 1
            # 栈帧：(顶点, 父边ID, 邻接迭代器)
            stack = [(root, None, iter(adj[root]))]
            while stack:
                u, p_edge, it = stack[-1]
                descended = False
                for v, eid in it:
                    if eid == p_edge:
                        continue  # 仅跳过来的那条树边，平行边照常处理
                    if v not in disc:
                        edge_stack.append(eid)
                        parent_edge[v] = eid
                        disc[v] = low[v] = timer
                        timer += 1
                        children[u] += 1
                        stack.append((v, eid, iter(adj[v])))
                        descended = True
                        break
                    if disc[v] < disc[u]:  # 回边（含指向父点的平行边）
                        edge_stack.append(eid)
                        if disc[v] < low[u]:
                            low[u] = disc[v]
                    # disc[v] > disc[u] 的边已从对侧处理过，忽略
                if descended:
                    continue
                stack.pop()
                if not stack:
                    break
                p = stack[-1][0]
                if low[u] < low[p]:
                    low[p] = low[u]
                tree_edge = parent_edge[u]
                if low[u] > disc[p]:
                    bridges.add(tree_edge)
                if low[u] >= disc[p]:
                    # 根的割点条件：DFS 树中孩子数 >= 2
                    if p != root or children[p] >= 2:
                        articulations.add(p)
                    block = []
                    while True:
                        eid = edge_stack.pop()
                        block.append(eid)
                        if eid == tree_edge:
                            break
                    blocks.append(frozenset(block))

        # 自环：各自单独构成一个双连通块，且永远不是桥
        for e in self._edges.values():
            if e.active_at(t) and e.is_loop():
                blocks.append(frozenset({e.edge_id}))

        # 连通分量（含孤立点）
        components = []
        seen = set()
        for v in sorted(self._vertices):
            if v not in seen:
                comp = self._bfs(v, adj)
                seen |= comp
                components.append(frozenset(comp))

        return Analysis(t, bridges, articulations, blocks, components)
