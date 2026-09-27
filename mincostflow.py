"""一般运输问题的精确最小费用流（SSP：连续最短路增广）。

供给/需求为整数，成本为整数（或任意可加的数），逐次沿最短路增广。
一般成本矩阵不满足一维 Monge 结构，不能用累计分布公式，必须真正求解。
"""

from __future__ import annotations

from typing import Dict, Hashable, Tuple

_INF = float("inf")


def min_cost_transport(
    supplies: Dict[Hashable, int],
    demands: Dict[Hashable, int],
    costs: Dict[Tuple[Hashable, Hashable], int],
) -> Tuple[Dict[Tuple[Hashable, Hashable], int], int]:
    """返回 (flow, total_cost)；flow 仅含正流量边。总量不一致时报错。"""
    total_supply = sum(supplies.values())
    if total_supply != sum(demands.values()):
        raise ValueError("supply and demand totals differ")
    if any(v < 0 for v in supplies.values()) or any(v < 0 for v in demands.values()):
        raise ValueError("negative supply/demand")
    skeys, dkeys = list(supplies), list(demands)
    n, m = len(skeys), len(dkeys)
    src, snk = 0, n + m + 1
    graph = [[] for _ in range(n + m + 2)]

    def add_edge(frm: int, to: int, cap: int, cost: int) -> None:
        graph[frm].append([to, cap, cost, len(graph[to])])
        graph[to].append([frm, 0, -cost, len(graph[frm]) - 1])

    for i, key in enumerate(skeys):
        add_edge(src, 1 + i, supplies[key], 0)
    for i, a in enumerate(skeys):
        for j, b in enumerate(dkeys):
            add_edge(1 + i, 1 + n + j, total_supply, costs[(a, b)])
    for j, key in enumerate(dkeys):
        add_edge(1 + n + j, snk, demands[key], 0)

    sent = 0
    while sent < total_supply:
        # Bellman-Ford（边数少，且允许任意成本符号）。
        dist = [_INF] * len(graph)
        parent = [None] * len(graph)
        dist[src] = 0
        for _ in range(len(graph) - 1):
            changed = False
            for u in range(len(graph)):
                if dist[u] == _INF:
                    continue
                for ei, e in enumerate(graph[u]):
                    if e[1] > 0 and dist[u] + e[2] < dist[e[0]]:
                        dist[e[0]] = dist[u] + e[2]
                        parent[e[0]] = (u, ei)
                        changed = True
            if not changed:
                break
        if dist[snk] == _INF:
            raise ValueError("no augmenting path: infeasible transportation problem")
        aug, node = total_supply - sent, snk
        while node != src:
            u, ei = parent[node]
            aug = min(aug, graph[u][ei][1])
            node = u
        node = snk
        while node != src:
            u, ei = parent[node]
            e = graph[u][ei]
            e[1] -= aug
            graph[node][e[3]][1] += aug
            node = u
        sent += aug

    flow: Dict[Tuple[Hashable, Hashable], int] = {}
    total_cost = 0
    for i, a in enumerate(skeys):
        for e in graph[1 + i]:
            to, _, cost, rev = e
            if 1 + n <= to < 1 + n + m:
                used = graph[to][rev][1]
                if used:
                    b = dkeys[to - (1 + n)]
                    flow[(a, b)] = used
                    total_cost += used * cost
    return flow, total_cost
