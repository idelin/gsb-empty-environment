"""确定性精确最小度排序：隐式商图 (quotient graph) 实现。

数据模型
--------
* 变量顶点 ``0 .. n-1``；每个已消元顶点 ``v`` 同时成为一个"元素"（element），
  其邻接变量集合表示消元时其后邻居构成的团（团的边不展开）。
* ``adj_var[v]``：变量 v 当前直连的变量集合；
* ``adj_el[v]``：与变量 v 相邻的元素集合；
* ``el_adj[e]``：元素 e 覆盖的变量集合。

变量 v 的精确当前度数 = | union(adj_var[v], U_{e in adj_el[v]} el_adj[e]) \\ {v} |。
多个元素覆盖同一邻居时集合自然去重，绝不用元素大小之和。

惰性堆：堆中键为 (degree, id)，id 保证并列时取最小原始 ID；每次出堆都重新
计算精确度数验证，过期键丢弃或修正重推。元素吸收 (absorption) 由集合包含
关系判定。
"""

from heapq import heappush, heappop


def _norm_edges(n, edges):
    seen = set()
    for edge in edges:
        u, v = edge[0], edge[1]
        if not (0 <= u < n and 0 <= v < n):
            raise ValueError(f"vertex out of range in edge {edge!r}")
        if u == v:
            continue  # 对角线位置不参与无向结构
        if u > v:
            u, v = v, u
        seen.add((u, v))
    return seen


class OrderingResult:
    """一次最小度排序的全部产物。"""

    def __init__(self, perm, steps, fill_edges, l_pattern, internal_peak):
        self.perm = perm                      # perm[k] = 第 k 步消元的原始顶点
        self.invperm = [0] * len(perm)        # invperm[v] = v 的消元位次
        for k, v in enumerate(perm):
            self.invperm[v] = k
        self.steps = steps                    # 逐步记录：度/活跃邻居/新增填充
        self.fill_edges = fill_edges          # 全部新增填充边（按原始顶点 ID）
        self.l_pattern = l_pattern            # l_pattern[k] = 第 k 列下方非零行位次集合
        self.internal_peak = internal_peak    # 商图内部内存峰值（见 memory_report）

    @property
    def n(self):
        return len(self.perm)

    def nnz_l(self, diagonal=True):
        off = sum(len(col) for col in self.l_pattern)
        return off + (self.n if diagonal else 0)

    def memory_report(self):
        """区分内部商图内存与最终产物内存。"""
        return {
            "internal_quotient_peak": self.internal_peak,
            "output_l_nonzeros": self.nnz_l(diagonal=True),
            "output_l_offdiagonal": self.nnz_l(diagonal=False),
            "output_fill_edges": len(self.fill_edges),
        }


def minimum_degree_order(n, edges):
    """对 n 个顶点的无向图执行确定性精确最小度排序。

    返回 :class:`OrderingResult`。与显式消元图参考算法逐等价：同一活跃图、
    同一最小度选择（并列取最小 ID）、同一团补边集合。
    """
    edges = _norm_edges(n, edges)

    adj_var = [set() for _ in range(n)]
    adj_el = [set() for _ in range(n)]
    el_adj = {}
    for u, v in edges:
        adj_var[u].add(v)
        adj_var[v].add(u)

    alive = [True] * n

    def neighborhood(v):
        nb = set(adj_var[v])
        for e in adj_el[v]:
            nb |= el_adj[e]
        nb.discard(v)
        return nb

    def adjacent(u, w):
        """商图中两个活变量是否在同一个团/直连（即显式图中是否已有边）。"""
        if w in adj_var[u]:
            return True
        return not adj_el[u].isdisjoint(adj_el[w])

    def internal_size():
        total = 0
        for s in adj_var:
            total += len(s)
        for s in adj_el:
            total += len(s)
        for s in el_adj.values():
            total += len(s)
        return total

    heap = []
    latest = [None] * n

    def push(v):
        d = len(neighborhood(v))
        latest[v] = d
        heappush(heap, (d, v))

    for v in range(n):
        push(v)

    perm = []
    steps = []
    fill_edges = set()
    pos_of = {}
    internal_peak = internal_size()

    for k in range(n):
        # ---- 惰性堆：出堆时必须验证精确当前度数 ----
        while True:
            d, v = heappop(heap)
            if not alive[v]:
                continue
            if latest[v] != d:
                continue
            exact = len(neighborhood(v))
            if exact != d:
                latest[v] = exact
                heappush(heap, (exact, v))
                continue
            break

        nb = neighborhood(v)
        nb_sorted = sorted(nb)

        # ---- 记录新增填充边（仅原始结构中不存在的非对角边，每条计一次）----
        step_fill = []
        for i in range(len(nb_sorted)):
            a = nb_sorted[i]
            for j in range(i + 1, len(nb_sorted)):
                b = nb_sorted[j]
                if not adjacent(a, b):
                    key = (a, b) if a < b else (b, a)
                    if key not in fill_edges:
                        fill_edges.add(key)
                        step_fill.append(key)

        pos_of[v] = k
        perm.append(v)
        steps.append({
            "step": k,
            "vertex": v,
            "degree": d,
            "active_neighbors": nb_sorted,
            "fill": step_fill,
        })

        # ---- 消元：v 成为新元素，邻居团隐式表示 ----
        alive[v] = False
        el_adj[v] = set(nb)
        for u in nb:
            adj_var[u].discard(v)
            adj_el[u].add(v)

        # 原先包含 v 的元素去掉 v；随后做吸收判定。
        for e in tuple(adj_el[v]):
            el_adj[e].discard(v)
        adj_el[v].clear()

        # 吸收冗余元素：若 el_adj[e] 被新元素覆盖，则 e 可删除。
        new_e = v
        for u in nb:
            for e in tuple(adj_el[u]):
                if e == new_e or e not in el_adj:
                    continue
                if el_adj[e] <= el_adj[new_e]:
                    cover = el_adj[e]
                    for w in cover:
                        adj_el[w].discard(e)
                    del el_adj[e]

        adj_var[v].clear()

        # 受影响变量重新计算精确度数并入堆
        for u in nb:
            if alive[u]:
                push(u)

        size = internal_size()
        if size > internal_peak:
            internal_peak = size

    l_pattern = [set() for _ in range(n)]
    for st in steps:
        k = st["step"]
        for u in st["active_neighbors"]:
            l_pattern[k].add(pos_of[u])

    return OrderingResult(perm, steps, fill_edges, l_pattern, internal_peak)
