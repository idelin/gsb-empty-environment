"""独立小图 oracle：直接构造显式消元图并完整重算。

本模块刻意与 :mod:`symchol.mindeg` / :mod:`symchol.incremental` 完全独立：
不使用商图、不使用惰性堆、不维护来源计数/缓存，每次调用都从零展开显式
邻接集并逐步补全团的全部边。仅供测试对拍使用（直接、朴素、易验证）。
"""


def normalize_edges(n, edges):
    seen = set()
    for edge in edges:
        u, v = edge[0], edge[1]
        if not (0 <= u < n and 0 <= v < n):
            raise ValueError(f"vertex out of range in edge {edge!r}")
        if u == v:
            continue
        if u > v:
            u, v = v, u
        seen.add((u, v))
    return seen


def oracle_order(n, edges):
    """显式消元图最小度排序，返回与 OrderingResult 对齐的字典。

    每一步线性扫描所有活跃顶点选度数最小（并列最小 ID），然后把后邻居
    两两补为显式边。复杂度 O(n^2 + n*|fill|)，纯粹用于参考。
    """
    adj = [set() for _ in range(n)]
    for u, v in normalize_edges(n, edges):
        adj[u].add(v)
        adj[v].add(u)

    alive = [True] * n
    perm = []
    steps = []
    fill_edges = set()

    for k in range(n):
        best = None
        for v in range(n):
            if alive[v]:
                d = len(adj[v])
                if best is None or d < best[0] or (d == best[0] and v < best[1]):
                    best = (d, v)
        d, v = best
        nb = sorted(adj[v])

        step_fill = []
        for i in range(len(nb)):
            a = nb[i]
            for j in range(i + 1, len(nb)):
                b = nb[j]
                if b not in adj[a]:
                    key = (a, b) if a < b else (b, a)
                    adj[a].add(b)
                    adj[b].add(a)
                    if key not in fill_edges:
                        fill_edges.add(key)
                        step_fill.append(key)

        perm.append(v)
        steps.append({
            "step": k,
            "vertex": v,
            "degree": d,
            "active_neighbors": nb,
            "fill": step_fill,
        })
        for u in nb:
            adj[u].discard(v)
        adj[v].clear()
        alive[v] = False

    pos_of = {v: k for k, v in enumerate(perm)}
    l_pattern = [set() for _ in range(n)]
    for st in steps:
        for u in st["active_neighbors"]:
            l_pattern[st["step"]].add(pos_of[u])

    return {
        "perm": perm,
        "steps": steps,
        "fill_edges": fill_edges,
        "l_pattern": l_pattern,
    }


def oracle_symbolic(n, edges, perm):
    """给定固定置换，完整重算 L 非零模式与消元树。

    直接按 perm 顺序玩消元游戏（后邻居补团），逐列收集位次。返回
    ``(rows, parent)``：rows[c] 为列 c 下方非零行位次集合；
    parent[c] = 列 c 中最小行位次（无则 None），即经典消元树父节点。
    """
    if sorted(perm) != list(range(n)):
        raise ValueError("perm must be a permutation of 0..n-1")
    pos = [0] * n
    for i, v in enumerate(perm):
        pos[v] = i

    adj = [set() for _ in range(n)]
    for u, v in normalize_edges(n, edges):
        adj[u].add(v)
        adj[v].add(u)

    rows = [set() for _ in range(n)]
    for k, v in enumerate(perm):
        later = [u for u in tuple(adj[v]) if pos[u] > k]
        rows[k] = {pos[u] for u in later}
        for i in range(len(later)):
            a = later[i]
            for j in range(i + 1, len(later)):
                b = later[j]
                adj[a].add(b)
                adj[b].add(a)
        for u in later:
            adj[u].discard(v)
        adj[v].clear()

    parent = [min(col) if col else None for col in rows]
    return rows, parent
