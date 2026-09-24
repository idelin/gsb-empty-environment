"""版本轴上的动态图：离线批量回答历史版本的连通性与分量大小查询。

模型
----
- 操作按版本（正整数）发生：加点、加边、删边。
- 每条边的存活期是左闭右开区间 [添加版本, 删除版本)；删除只影响后续版本，
  即在删除版本上该边已经不存在。
- 顶点、边都使用字符串 ID；允许重边（不同边 ID 连接同一对顶点）；
  孤立点始终保留（分量大小为 1）。
- 查询（连通性 / 分量大小）针对任意历史版本批量提交，统一离线求解。

算法
----
不为每个查询复制整张图。采用「线段树套时间 + 可回滚并查集」：

1. 把每条边的存活区间插入版本轴上的线段树，每条边只出现在 O(log V) 个节点；
2. 对线段树做 DFS，进入节点时应用该节点上的所有边，离开时回滚；
3. 到达叶子（即某个版本）时，并查集恰好表示该版本的图，批量回答该版本的全部查询。

总复杂度 O((E log V + Q) α(N))，空间 O(E log V + N)。
"""

from __future__ import annotations

from typing import Any, Hashable

__all__ = ["RollbackDSU", "VersionedGraph", "QueryResult"]


class RollbackDSU:
    """可回滚并查集：按大小合并、不做路径压缩，支持撤销到历史标记点。"""

    def __init__(self) -> None:
        self._parent: dict[Hashable, Hashable] = {}
        self._size: dict[Hashable, int] = {}
        # 历史栈：元素为 None（本次合并未改变集合）或 (被挂根, 新根)
        self._history: list[tuple[Hashable, Hashable] | None] = []

    def add_vertex(self, v: Hashable) -> None:
        if v not in self._parent:
            self._parent[v] = v
            self._size[v] = 1

    def __contains__(self, v: Hashable) -> bool:
        return v in self._parent

    def find(self, v: Hashable) -> Hashable:
        while self._parent[v] != v:
            v = self._parent[v]
        return v

    def union(self, a: Hashable, b: Hashable) -> None:
        self.add_vertex(a)
        self.add_vertex(b)
        ra, rb = self.find(a), self.find(b)
        if ra == rb:
            self._history.append(None)
            return
        if self._size[ra] < self._size[rb]:
            ra, rb = rb, ra
        self._history.append((rb, ra))
        self._parent[rb] = ra
        self._size[ra] += self._size[rb]

    def mark(self) -> int:
        """记录当前历史栈深度，供之后回滚。"""
        return len(self._history)

    def rollback(self, mark: int) -> None:
        """撤销到 mark 时刻的所有合并。"""
        while len(self._history) > mark:
            rec = self._history.pop()
            if rec is None:
                continue
            child, root = rec
            self._size[root] -= self._size[child]
            self._parent[child] = child

    def connected(self, a: Hashable, b: Hashable) -> bool:
        return self.find(a) == self.find(b)

    def component_size(self, v: Hashable) -> int:
        return self._size[self.find(v)]


class QueryResult:
    """一条查询的结果。"""

    __slots__ = ("kind", "version", "vertices", "value")

    def __init__(self, kind: str, version: int, vertices: tuple[str, ...], value: Any):
        self.kind = kind            # "connected" 或 "component_size"
        self.version = version
        self.vertices = vertices
        self.value = value

    def __repr__(self) -> str:  # pragma: no cover - 仅用于展示
        args = ", ".join(self.vertices)
        return f"v{self.version}: {self.kind}({args}) = {self.value}"


class VersionedGraph:
    """带版本轴的动态图，支持离线批量历史查询。

    使用方式：依次调用 add_vertex / add_edge / remove_edge 记录操作，
    用 query_connected / query_component_size 登记查询（返回查询句柄），
    最后调用 solve() 一次性得到全部结果（按登记顺序）。
    """

    def __init__(self) -> None:
        self._vertices: set[str] = set()
        self._active_edges: dict[str, tuple[str, str, int]] = {}  # id -> (u, v, add_version)
        self._intervals: list[tuple[int, int, tuple[str, str]]] = []  # [l, r), (u, v)
        self._queries: list[tuple[int, str, tuple[str, ...]]] = []
        self._max_version = 0
        self._solved = False

    # ------------------------------------------------------------------ 操作
    def _touch_version(self, version: int) -> None:
        if not isinstance(version, int) or version < 1:
            raise ValueError(f"版本必须是正整数，得到 {version!r}")
        if self._solved:
            raise RuntimeError("solve() 之后不能再登记操作或查询")
        self._max_version = max(self._max_version, version)

    def add_vertex(self, vertex_id: str, version: int) -> None:
        """在 version 版本显式加入顶点（孤立点也会被保留）。"""
        self._touch_version(version)
        self._vertices.add(vertex_id)

    def add_edge(self, edge_id: str, u: str, v: str, version: int) -> None:
        """在 version 版本加入边 edge_id: u -- v（端点不存在则自动建点）。"""
        self._touch_version(version)
        if edge_id in self._active_edges:
            raise ValueError(f"边 {edge_id!r} 当前已存在")
        self._vertices.update((u, v))
        self._active_edges[edge_id] = (u, v, version)

    def remove_edge(self, edge_id: str, version: int) -> None:
        """在 version 版本删除边；删除只影响后续版本（区间左闭右开）。"""
        self._touch_version(version)
        try:
            u, v, add_version = self._active_edges.pop(edge_id)
        except KeyError:
            raise KeyError(f"边 {edge_id!r} 不存在或已被删除") from None
        self._intervals.append((add_version, version, (u, v)))

    # ------------------------------------------------------------------ 查询
    def query_connected(self, u: str, v: str, version: int) -> int:
        """登记查询：version 版本时 u 与 v 是否连通。返回查询句柄。"""
        self._touch_version(version)
        self._queries.append((version, "connected", (u, v)))
        return len(self._queries) - 1

    def query_component_size(self, u: str, version: int) -> int:
        """登记查询：version 版本时 u 所在连通分量的大小。返回查询句柄。"""
        self._touch_version(version)
        self._queries.append((version, "component_size", (u,)))
        return len(self._queries) - 1

    # ------------------------------------------------------------------ 求解
    def solve(self) -> list[QueryResult]:
        """离线批量回答所有已登记查询，结果顺序与登记顺序一致。"""
        self._solved = True
        n = self._max_version
        # 仍在存的边：存活到时间轴末尾（左闭右开，r 取 n+1）
        for u, v, add_version in self._active_edges.values():
            self._intervals.append((add_version, n + 1, (u, v)))
        self._active_edges.clear()

        results: list[QueryResult] = []
        for version, kind, args in self._queries:
            for x in args:
                if x not in self._vertices:
                    raise KeyError(f"查询了从未出现过的顶点 {x!r}")
            results.append(QueryResult(kind, version, args, None))
        if n == 0 or not self._queries:
            return results

        # 1) 区间插入线段树（版本轴 [1, n]）
        tree: list[list[tuple[str, str]]] = [[] for _ in range(4 * n + 4)]

        def add_interval(node: int, nl: int, nr: int,
                         l: int, r: int, edge: tuple[str, str]) -> None:
            if r < nl or nr < l:
                return
            if l <= nl and nr <= r:
                tree[node].append(edge)
                return
            mid = (nl + nr) // 2
            add_interval(2 * node, nl, mid, l, r, edge)
            add_interval(2 * node + 1, mid + 1, nr, l, r, edge)

        for l, r, edge in self._intervals:
            if l < r:  # 空区间（同版本加又删）直接跳过
                add_interval(1, 1, n, l, r - 1, edge)  # 左闭右开 -> 闭区间

        # 2) 查询按版本分桶
        queries_at: dict[int, list[int]] = {}
        for i, (version, _kind, _args) in enumerate(self._queries):
            queries_at.setdefault(version, []).append(i)

        # 3) DFS 线段树 + 可回滚并查集，在叶子处回答该版本的全部查询
        dsu = RollbackDSU()
        for vtx in self._vertices:
            dsu.add_vertex(vtx)

        def dfs(node: int, nl: int, nr: int) -> None:
            mark = dsu.mark()
            for u, w in tree[node]:
                dsu.union(u, w)
            if nl == nr:
                for qi in queries_at.get(nl, ()):
                    res = results[qi]
                    if res.kind == "connected":
                        res.value = dsu.connected(res.vertices[0], res.vertices[1])
                    else:
                        res.value = dsu.component_size(res.vertices[0])
            else:
                mid = (nl + nr) // 2
                dfs(2 * node, nl, mid)
                dfs(2 * node + 1, mid + 1, nr)
            dsu.rollback(mark)

        dfs(1, 1, n)
        return results
