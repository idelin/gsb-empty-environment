"""Bellman-Ford 求解器：超源点零初始化，输出最大势值或负环证据。"""
from __future__ import annotations


def bellman_ford(vertices, edges):
    """求解差分约束组 x_v - x_u <= c（对应边 u -> v，权 c）。

    vertices: 顶点字符串 ID 的可迭代对象（含孤立点）。
    edges:    (cid, u, v, c) 四元组的可迭代对象，允许重边与自环。

    返回 (feasible, potentials, cycle)：
      可行   -> (True,  {顶点: 势值}, None)，势值为逐点最大可行解（均 <= 0）；
      不可行 -> (False, None, [构成负环的约束 ID，按环的方向排列])。
    零权环不会误报：仅严格小于才松弛。大整数由 Python int 原生支持。
    """
    dist = {v: 0 for v in vertices}
    for _, u, v, _ in edges:
        dist.setdefault(u, 0)
        dist.setdefault(v, 0)
    n = len(dist)
    if n == 0:
        return True, {}, None

    pred = {}  # 顶点 -> 最后一次松弛它所用边的下标
    last = None
    for _ in range(n):
        last = None
        for idx, (_cid, u, v, c) in enumerate(edges):
            if dist[u] + c < dist[v]:
                dist[v] = dist[u] + c
                pred[v] = idx
                last = v
        if last is None:
            return True, dist, None

    # 第 n 轮仍可松弛 => 存在负环。沿前驱走 n 步必进入环上。
    x = last
    for _ in range(n):
        x = edges[pred[x]][1]
    cycle = []
    cur = x
    while True:
        idx = pred[cur]
        cycle.append(edges[idx][0])
        cur = edges[idx][1]
        if cur == x:
            break
    cycle.reverse()
    return False, None, cycle
