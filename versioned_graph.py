"""版本化图：边在版本轴上增删，离线批量回答历史版本的连通性 / 分量大小查询。

模型约定
--------
- 顶点、边均使用字符串 ID；顶点可显式 ``add_vertex`` 注册（孤立点也计入分量），
  被边引用时会自动注册。
- 边允许重边（多重边）与自环；边 ID 全局唯一，删除按 ID 进行，
  删除一条平行边不影响其余平行边。
- 时间区间左闭右开：在版本 ``a`` 加入、版本 ``d`` 删除的边，
  存活区间为 ``[a, d)`` —— 删除只影响 ``d`` 及之后的版本。
- 顶点自注册版本起一直存在（不支持删点）。
- 查询先批量注册，再调用 :meth:`VersionedGraph.execute` 一次性求解；
  查询为纯读操作，同版本内注册顺序不影响结果。

实现原理（不为每个查询复制整张图）
--------------------------------
离线算法：版本轴线段树 + 可回滚并查集（按大小合并、无路径压缩）。
每条边 / 每个顶点的存活区间被挂到线段树的 O(log T) 个节点上，
随后对线段树做一次 DFS，进入节点时应用修改、离开时回滚，
在叶子处回答该版本的所有查询。

复杂度：设 E 为边事件数、V 为顶点数、Q 为查询数、T 为不同版本数，
时间 O((V + E) log T + Q α)，空间 O(V + E log T + Q)。
"""

from __future__ import annotations

from bisect import bisect_left

__all__ = ["VersionedGraph"]

_CONNECTED = "connected"
_COMPONENT_SIZE = "component_size"


def _check_id(value: str, what: str) -> str:
    if not isinstance(value, str) or not value:
        raise ValueError(f"{what} 必须是非空字符串，得到 {value!r}")
    return value


def _check_version(version: int) -> int:
    if not isinstance(version, int) or isinstance(version, bool) or version < 0:
        raise ValueError(f"版本号必须是非负整数，得到 {version!r}")
    return version


class _RollbackDSU:
    """可回滚并查集，额外跟踪每个分量中“已激活顶点”的数量。"""

    __slots__ = ("parent", "size", "active", "_log")

    def __init__(self) -> None:
        self.parent: dict[str, str] = {}
        self.size: dict[str, int] = {}      # 仅根节点有效：分量内激活顶点数
        self.active: dict[str, bool] = {}
        self._log: list[tuple] = []

    def _ensure(self, v: str) -> None:
        if v not in self.parent:
            self.parent[v] = v
            self.size[v] = 0
            self.active[v] = False

    def find(self, v: str) -> str:
        parent = self.parent
        while parent[v] != v:
            v = parent[v]
        return v

    def activate(self, v: str) -> None:
        self._ensure(v)
        root = self.find(v)
        self.active[v] = True
        self.size[root] += 1
        self._log.append(("activate", v, root))

    def union(self, u: str, v: str) -> None:
        self._ensure(u)
        self._ensure(v)
        ru, rv = self.find(u), self.find(v)
        if ru == rv:
            self._log.append(("noop",))
            return
        if self.size[ru] > self.size[rv]:
            ru, rv = rv, ru
        self.parent[ru] = rv
        self.size[rv] += self.size[ru]
        self._log.append(("union", ru, rv))

    def checkpoint(self) -> int:
        return len(self._log)

    def rollback(self, checkpoint: int) -> None:
        while len(self._log) > checkpoint:
            op = self._log.pop()
            kind = op[0]
            if kind == "union":
                _, child, root = op
                self.parent[child] = child
                self.size[root] -= self.size[child]
            elif kind == "activate":
                _, v, root = op
                self.active[v] = False
                self.size[root] -= 1
            # "noop" 无需处理


class VersionedGraph:
    """支持版本轴边增删与离线批量连通性查询的多重图。"""

    def __init__(self) -> None:
        self._birth: dict[str, int] = {}
        # edge_id -> [u, v, add_version, delete_version | None]
        self._edges: dict[str, list] = {}
        self._queries: list[tuple] = []

    # ------------------------------------------------------------------ 事件
    def add_vertex(self, vertex_id: str, version: int) -> None:
        """注册顶点，自 ``version`` 起存在（允许孤立点）。重复注册取最早版本。"""
        _check_id(vertex_id, "顶点 ID")
        version = _check_version(version)
        prev = self._birth.get(vertex_id)
        if prev is None or version < prev:
            self._birth[vertex_id] = version

    def add_edge(self, edge_id: str, u: str, v: str, version: int) -> None:
        """在 ``version`` 加入边 ``u--v``。允许重边与自环；边 ID 全局唯一。"""
        _check_id(edge_id, "边 ID")
        _check_id(u, "顶点 ID")
        _check_id(v, "顶点 ID")
        version = _check_version(version)
        if edge_id in self._edges:
            raise ValueError(f"边 ID {edge_id!r} 已存在")
        self._edges[edge_id] = [u, v, version, None]
        self._touch(u, version)
        self._touch(v, version)

    def remove_edge(self, edge_id: str, version: int) -> None:
        """在 ``version`` 删除边；边存活区间为左闭右开 [add, delete)。"""
        _check_id(edge_id, "边 ID")
        version = _check_version(version)
        rec = self._edges.get(edge_id)
        if rec is None:
            raise KeyError(f"边 {edge_id!r} 不存在")
        if rec[3] is not None:
            raise ValueError(f"边 {edge_id!r} 已被删除过")
        if version <= rec[2]:
            raise ValueError(
                f"删除版本 {version} 必须晚于加入版本 {rec[2]}（区间左闭右开）"
            )
        rec[3] = version

    # ------------------------------------------------------------------ 查询
    def query_connected(self, version: int, u: str, v: str) -> None:
        """注册查询：版本 ``version`` 时 ``u`` 与 ``v`` 是否连通。"""
        version = _check_version(version)
        self._queries.append((_CONNECTED, version, (u, v)))

    def query_component_size(self, version: int, u: str) -> None:
        """注册查询：版本 ``version`` 时 ``u`` 所在连通分量的顶点数。"""
        version = _check_version(version)
        self._queries.append((_COMPONENT_SIZE, version, (u,)))

    # ------------------------------------------------------------------ 求解
    def execute(self) -> list:
        """一次性回答全部已注册查询，按注册顺序返回结果列表。

        连通性查询返回 ``bool``，分量大小查询返回 ``int``；
        查询该版本尚不存在的顶点时分别返回 ``False`` / ``0``。
        执行后查询队列被清空，可继续注册下一批。
        """
        queries, self._queries = self._queries, []
        results: list = [None] * len(queries)
        if not queries:
            return results

        # 版本坐标压缩：只有事件 / 查询出现的版本才需要叶子。
        xs = set(self._birth.values())
        for _, _, add, delete in self._edges.values():
            xs.add(add)
            if delete is not None:
                xs.add(delete)
        for _, version, _ in queries:
            xs.add(version)
        xs = sorted(xs)
        n = len(xs)

        size = 1
        while size < n:
            size <<= 1
        tree: list[list[tuple]] = [[] for _ in range(2 * size)]

        def add_item(node: int, lo: int, hi: int, ql: int, qr: int, item: tuple) -> None:
            if ql <= lo and hi <= qr:
                tree[node].append(item)
                return
            mid = (lo + hi) // 2
            if ql < mid:
                add_item(node * 2, lo, mid, ql, qr, item)
            if qr > mid:
                add_item(node * 2 + 1, mid, hi, ql, qr, item)

        for vid, birth in self._birth.items():
            add_item(1, 0, size, bisect_left(xs, birth), n, ("vertex", vid))
        for u, v, add, delete in self._edges.values():
            qr = n if delete is None else bisect_left(xs, delete)
            add_item(1, 0, size, bisect_left(xs, add), qr, ("edge", u, v))

        by_version: dict[int, list[tuple]] = {}
        for idx, (kind, version, args) in enumerate(queries):
            by_version.setdefault(version, []).append((idx, kind, args))

        dsu = _RollbackDSU()

        def answer(kind: str, args: tuple):
            if kind == _CONNECTED:
                u, v = args
                return (
                    dsu.active.get(u, False)
                    and dsu.active.get(v, False)
                    and dsu.find(u) == dsu.find(v)
                )
            (u,) = args
            if not dsu.active.get(u, False):
                return 0
            return dsu.size[dsu.find(u)]

        def dfs(node: int, lo: int, hi: int) -> None:
            checkpoint = dsu.checkpoint()
            for item in tree[node]:
                if item[0] == "vertex":
                    dsu.activate(item[1])
                else:
                    dsu.union(item[1], item[2])
            if lo + 1 == hi:
                if lo < n:
                    for idx, kind, args in by_version.get(xs[lo], ()):
                        results[idx] = answer(kind, args)
            else:
                mid = (lo + hi) // 2
                dfs(node * 2, lo, mid)
                dfs(node * 2 + 1, mid, hi)
            dsu.rollback(checkpoint)

        dfs(1, 0, size)
        return results

    # ------------------------------------------------------------------ 内部
    def _touch(self, vertex_id: str, version: int) -> None:
        prev = self._birth.get(vertex_id)
        if prev is None or version < prev:
            self._birth[vertex_id] = version
