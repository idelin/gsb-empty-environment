"""独立的逐区间全量 Bellman-Ford 参考实现，用于交叉核对主系统。

与主系统的分段缓存求解不同，这里对每个原子分段从零构建完整图
并跑到不动点，作为主系统结果的对照基准。
"""
from __future__ import annotations


def reference_bellman_ford(vertices, edges):
    """全量 Bellman-Ford：返回 (feasible, potentials_or_None)。

    与 solver.bellman_ford 相互独立地实现：反复全量松弛直至稳定，
    稳定后再做一轮检测。最大势值解唯一，因此可与主系统逐点比较。
    """
    dist = {v: 0 for v in vertices}
    for _, u, v, _ in edges:
        dist.setdefault(u, 0)
        dist.setdefault(v, 0)
    n = len(dist)
    for _ in range(n):
        changed = False
        for _cid, u, v, c in edges:
            if dist[u] + c < dist[v]:
                dist[v] = dist[u] + c
                changed = True
        if not changed:
            break
    for _cid, u, v, c in edges:
        if dist[u] + c < dist[v]:
            return False, None
    return True, dist


def check_window(system, t0, t1):
    """逐原子分段核对主系统结果。返回问题描述列表，空列表表示全部一致。"""
    problems = []
    for s, e in system.atomic_segments(t0, t1):
        active = [c for c in system.active_constraints(s, e)
                  if c.cid not in system.revoked]
        edges = [(c.cid, c.u, c.v, c.c) for c in active]
        ref_feasible, ref_pot = reference_bellman_ford(system.vertices, edges)
        got = system.segment_result(s, e)
        tag = f"[{s}, {e})"
        if got.feasible != ref_feasible:
            problems.append(f"{tag} 可行性不一致: 主={got.feasible} 参考={ref_feasible}")
        elif ref_feasible and got.potentials != ref_pot:
            problems.append(f"{tag} 势值不一致: 主={got.potentials} 参考={ref_pot}")
        elif not ref_feasible:
            by_id = {c.cid: c for c in active}
            try:
                cyc = [by_id[i] for i in got.cycle]
            except KeyError as exc:
                problems.append(f"{tag} 负环含非活跃约束: {exc}")
                continue
            total = sum(c.c for c in cyc)
            ring = all(cyc[i].v == cyc[(i + 1) % len(cyc)].u for i in range(len(cyc)))
            if not ring:
                problems.append(f"{tag} 证据不构成环: {got.cycle}")
            elif total >= 0:
                problems.append(f"{tag} 环权非负: {got.cycle} 权和={total}")
    return problems
