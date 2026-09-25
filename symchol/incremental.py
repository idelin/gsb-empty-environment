"""固定置换下的增量符号 Cholesky 分析器。

数学模型（位置均指置换后的位次，行 > 列）：
L 的非对角非零位置集合是满足如下两条的最小闭集：
  (E) 原始结构边 {u,v} 给出位置 (max(pos[u],pos[v]), min(pos[u],pos[v]))；
  (F) 若 (r1,c) 与 (r2,c) 都在 L 中，则 (max(r1,r2), min(r1,r2)) 也在 L 中
      （消元第 c 列时其后邻居补团）。

可追溯来源（provenance）：
每个位置 (r,c) 维护 support 集合：
  ("e", u, v)  —— 由当前存在的原始边 {u,v} 直接支撑；
  ("f", k)     —— 由第 k 列填充支撑，当且仅当 (r,k) 与 (c,k) 当前都在 L 中。
位置存在 <=> support 非空。删除一条原始边只移除其对应来源；只要还有其它
路径（别的原始边或某列填充）支撑，位置保留。

增量传播用依赖工作队列，只访问受影响的列；最坏情况（影响沿消元树一路
传到根）允许退化为接近全图，但统计信息会如实报告实际访问列与重算条目。

事务原子性：整批校验通过后才应用；应用过程中任何异常都会完整回滚到
事务前状态。reorder() 是独立显式操作，固定置换的增量更新绝不改变顺序。
"""

from collections import deque
from copy import deepcopy

from .mindeg import minimum_degree_order
from .oracle import normalize_edges


class Snapshot:
    """只读快照：持有独立的深拷贝，之后对分析器的任何修改都不影响它。"""

    def __init__(self, n, perm, rows, parent, edges):
        self.n = n
        self.perm = tuple(perm)
        self._rows = tuple(frozenset(col) for col in rows)
        self._parent = tuple(parent)
        self.edges = frozenset(edges)

    def l_pattern(self):
        return self._rows

    def etree(self):
        return self._parent

    def nnz_l(self, diagonal=True):
        off = sum(len(c) for c in self._rows)
        return off + (self.n if diagonal else 0)


class IncrementalSymbolicCholesky:
    def __init__(self, n, edges, perm=None):
        self.n = n
        self.edges = normalize_edges(n, edges)
        self.perm = list(perm) if perm is not None else list(range(n))
        if sorted(self.perm) != list(range(n)):
            raise ValueError("perm must be a permutation of 0..n-1")
        self._set_pos()
        self.rows = [set() for _ in range(n)]
        self.support = {}
        self.parent = [None] * n
        self.log = []
        self._seq = 0
        self._rebuild(kind="init")

    # ------------------------------------------------------------------ 基础

    def _set_pos(self):
        self.pos = [0] * self.n
        for i, v in enumerate(self.perm):
            self.pos[v] = i

    def _rebuild(self, kind):
        """完整符号分解（仅用于初始化与显式 reorder）。"""
        n = self.n
        adj = [set() for _ in range(n)]
        for u, v in self.edges:
            adj[u].add(v)
            adj[v].add(u)
        rows = [set() for _ in range(n)]
        for k, v in enumerate(self.perm):
            later = [u for u in tuple(adj[v]) if self.pos[u] > k]
            rows[k] = {self.pos[u] for u in later}
            for i in range(len(later)):
                a = later[i]
                for j in range(i + 1, len(later)):
                    b = later[j]
                    adj[a].add(b)
                    adj[b].add(a)
            for u in later:
                adj[u].discard(v)
            adj[v].clear()
        self.rows = rows
        self.support = {}
        for u, v in self.edges:
            r, c = max(self.pos[u], self.pos[v]), min(self.pos[u], self.pos[v])
            self.support.setdefault((r, c), set()).add(("e", u, v))
        for c in range(n):
            col = sorted(rows[c])
            for i in range(len(col)):
                for j in range(i + 1, len(col)):
                    key = (col[j], col[i])
                    self.support.setdefault(key, set()).add(("f", c))
        self.parent = [min(col) if col else None for col in rows]
        self.log.append({
            "seq": self._seq, "type": kind,
            "edges": sorted(self.edges), "perm": list(self.perm),
        })
        self._seq += 1

    # ------------------------------------------------------------ 来源维护

    def _add_source(self, r, c, src, addq, stats):
        sup = self.support.get((r, c))
        if sup is not None and src in sup:
            return
        if sup is None:
            sup = set()
            self.support[(r, c)] = sup
            self.rows[c].add(r)
            addq.append((r, c))
            stats["added"].append((r, c))
            stats["visited"].add(c)
        sup.add(src)

    def _remove_source(self, r, c, src, delq, stats):
        sup = self.support.get((r, c))
        if not sup or src not in sup:
            return
        sup.discard(src)
        if not sup:
            del self.support[(r, c)]
            self.rows[c].discard(r)
            delq.append((r, c))
            stats["removed"].append((r, c))
            stats["visited"].add(c)

    def _drain_add(self, addq, stats):
        while addq:
            a, b = addq.popleft()
            for r2 in tuple(self.rows[b]):
                if r2 == a:
                    continue
                rr, cc = (a, r2) if a > r2 else (r2, a)
                self._add_source(rr, cc, ("f", b), addq, stats)

    def _drain_del(self, delq, stats):
        # gone[b]：本次级联中已从列 b 删除的行。同列多个位置可能在同一
        # 级联中相继删除，若只看当前 rows[b]，两个已删行之间的配对来源
        # 会被漏清；因此配对候选 = 当前行 ∪ 已删行。
        gone = {}
        while delq:
            a, b = delq.popleft()
            candidates = self.rows[b] | gone.get(b, set())
            for r2 in tuple(candidates):
                if r2 == a:
                    continue
                rr, cc = (a, r2) if a > r2 else (r2, a)
                self._remove_source(rr, cc, ("f", b), delq, stats)
            gone.setdefault(b, set()).add(a)

    # ------------------------------------------------------------ 公共 API

    def update(self, add=(), delete=()):
        """原子地应用一批结构边增删（固定置换）。返回实际访问统计。"""
        add = list(add)
        delete = list(delete)
        add_n = normalize_edges(self.n, add)
        del_n = normalize_edges(self.n, delete)
        for u, v in add:
            if u == v:
                raise ValueError(f"self-loop edge {(u, v)!r} is not allowed")
        overlap = add_n & del_n
        if overlap:
            raise ValueError(f"edges both added and deleted: {sorted(overlap)!r}")
        missing = del_n - self.edges
        if missing:
            raise ValueError(f"cannot delete absent edges: {sorted(missing)!r}")
        add_n -= self.edges  # 重复边合并：已存在的边是 no-op

        backup = (set(self.edges), [set(c) for c in self.rows],
                  deepcopy(self.support), list(self.parent))
        stats = {"visited": set(), "added": [], "removed": []}
        try:
            for u, v in del_n:
                self.edges.discard((u, v))
                r, c = max(self.pos[u], self.pos[v]), min(self.pos[u], self.pos[v])
                delq = deque()
                self._remove_source(r, c, ("e", u, v), delq, stats)
                self._drain_del(delq, stats)
            addq = deque()
            for u, v in add_n:
                self.edges.add((u, v))
                r, c = max(self.pos[u], self.pos[v]), min(self.pos[u], self.pos[v])
                self._add_source(r, c, ("e", u, v), addq, stats)
            self._drain_add(addq, stats)
            etree_updates = []
            for c in sorted(stats["visited"]):
                newp = min(self.rows[c]) if self.rows[c] else None
                if newp != self.parent[c]:
                    self.parent[c] = newp
                    etree_updates.append((c, newp))
        except Exception:
            self.edges, self.rows, self.support, self.parent = backup
            raise

        record = {
            "seq": self._seq, "type": "update",
            "add": sorted(add_n), "delete": sorted(del_n),
            "visited_columns": sorted(stats["visited"]),
            "added_positions": sorted(stats["added"]),
            "removed_positions": sorted(stats["removed"]),
            "etree_updates": etree_updates,
        }
        self._seq += 1
        self.log.append(record)
        return record

    def reorder(self):
        """显式重排序：对当前结构重跑最小度并整体重建符号分解。"""
        result = minimum_degree_order(self.n, self.edges)
        self.perm = list(result.perm)
        self._set_pos()
        self._rebuild(kind="reorder")
        return result

    def snapshot(self):
        return Snapshot(self.n, self.perm, self.rows, self.parent, self.edges)

    def l_pattern(self):
        return [set(c) for c in self.rows]

    def etree(self):
        return list(self.parent)

    # ------------------------------------------------------------ 日志重放

    @classmethod
    def replay(cls, n, log):
        """按可重放日志从零重建分析器（确定性）。"""
        first = log[0]
        assert first["type"] in ("init", "reorder")
        obj = cls(n, first["edges"], first["perm"])
        for rec in log[1:]:
            if rec["type"] == "update":
                obj.update(add=rec["add"], delete=rec["delete"])
            elif rec["type"] == "reorder":
                obj.reorder()
            else:  # pragma: no cover
                raise ValueError(f"unknown log record {rec['type']!r}")
        return obj
