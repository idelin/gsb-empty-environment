"""Exact min-cost max-flow with lexicographic tuple costs (stdlib only).

Costs are tuples ``(Decimal, int)`` compared lexicographically, which encodes
the objective hierarchy (total time difference, then pair-ID lexicographic
tie-break) exactly, with no floating-point rounding.
"""
from __future__ import annotations

import heapq
from decimal import Decimal

ZERO = (Decimal(0), 0)


def _neg(cost):
    return (-cost[0], -cost[1])


def _add(a, b):
    return (a[0] + b[0], a[1] + b[1])


def min_cost_max_flow(n_nodes, source, sink, arcs):
    """Successive shortest augmenting paths with potentials (Dijkstra).

    ``arcs``: iterable of ``(u, v, capacity, cost)`` with non-negative tuple
    costs.  Returns ``(flow_value, graph, forward_arc_refs)`` where
    ``forward_arc_refs[i]`` is the residual arc object for ``arcs[i]``; its
    remaining capacity is 0 iff the arc carries flow.
    """
    graph = [[] for _ in range(n_nodes)]

    def add_arc(u, v, cap, cost):
        fwd = [v, cap, cost, len(graph[v])]
        rev = [u, 0, _neg(cost), len(graph[u])]
        graph[u].append(fwd)
        graph[v].append(rev)
        return fwd

    refs = [add_arc(u, v, cap, cost) for (u, v, cap, cost) in arcs]

    potential = [ZERO] * n_nodes
    flow = 0
    while True:
        dist = [None] * n_nodes
        prev_node = [None] * n_nodes
        prev_edge = [None] * n_nodes
        dist[source] = ZERO
        pq = [(ZERO, source)]
        while pq:
            d, u = heapq.heappop(pq)
            if d != dist[u]:
                continue
            for e in graph[u]:
                if e[1] <= 0:
                    continue
                v = e[0]
                rc = _add(e[2], (potential[u][0] - potential[v][0],
                                 potential[u][1] - potential[v][1]))
                nd = _add(d, rc)
                if dist[v] is None or nd < dist[v]:
                    dist[v] = nd
                    prev_node[v] = u
                    prev_edge[v] = e
                    heapq.heappush(pq, (nd, v))
        if dist[sink] is None:
            break
        for v in range(n_nodes):
            if dist[v] is not None:
                potential[v] = _add(potential[v], dist[v])
        # Bottleneck is always 1 here (unit capacities out of the source),
        # but compute it generally.
        delta = None
        v = sink
        while v != source:
            e = prev_edge[v]
            delta = e[1] if delta is None else min(delta, e[1])
            v = prev_node[v]
        v = sink
        while v != source:
            e = prev_edge[v]
            e[1] -= delta
            graph[v][e[3]][1] += delta
            v = prev_node[v]
        flow += delta
    return flow, graph, refs


def has_negative_cycle(graph, n_nodes):
    """Bellman-Ford negative-cycle detection on the residual graph.

    A min-cost flow is cost-optimal iff its residual graph contains no
    negative-cost cycle.  All distances start at zero, which is equivalent to
    a super-source connected to every node.
    """
    dist = [ZERO] * n_nodes
    for _ in range(n_nodes):
        updated = False
        for u in range(n_nodes):
            du = dist[u]
            for e in graph[u]:
                if e[1] > 0:
                    nd = _add(du, e[2])
                    if nd < dist[e[0]]:
                        dist[e[0]] = nd
                        updated = True
        if not updated:
            return False
    return True
