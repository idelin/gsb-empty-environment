"""Exact min-cost circulation with lower bounds, implemented from scratch.

No external solver is used. All arithmetic is exact (fractions.Fraction), so
results are exact for integer/rational input data.

Algorithm:
  1. Feasibility: standard lower-bound reduction + Edmonds-Karp max flow.
  2. Optimality:  negative-cost cycle canceling (Bellman-Ford). A feasible
     circulation is min-cost iff its residual graph has no negative cycle.
     With rational data every augmentation strictly decreases the cost by a
     positive rational bounded away from zero, so the loop terminates.
"""

from __future__ import annotations

from collections import deque
from fractions import Fraction


class _Edge:
    __slots__ = ("to", "rev", "cap", "cost")

    def __init__(self, to: int, rev: int, cap: Fraction, cost: Fraction):
        self.to = to
        self.rev = rev
        self.cap = cap
        self.cost = cost


class _ArcRecord:
    __slots__ = ("u", "v", "lower", "upper", "cost", "edge")

    def __init__(self, u, v, lower, upper, cost):
        self.u = u
        self.v = v
        self.lower = lower
        self.upper = upper
        self.cost = cost
        self.edge = None


class Circulation:
    """Min-cost circulation on a graph with per-arc lower/upper bounds."""

    def __init__(self, num_nodes: int):
        self._n = num_nodes
        self.graph = [[] for _ in range(num_nodes)]
        self._records: list[_ArcRecord] = []
        self._built = False

    def add_arc(self, u: int, v: int, lower: Fraction, upper: Fraction,
                cost: Fraction) -> int:
        """Add arc u->v with lower <= flow <= upper; returns its record index."""
        if lower < 0 or upper < lower:
            raise ValueError(f"invalid arc bounds [{lower}, {upper}]")
        self._records.append(_ArcRecord(u, v, lower, upper, cost))
        return len(self._records) - 1

    def _add_residual_arc(self, u: int, v: int, cap: Fraction,
                          cost: Fraction) -> _Edge:
        fwd = _Edge(v, len(self.graph[v]), cap, cost)
        rev = _Edge(u, len(self.graph[u]), Fraction(0), -cost)
        self.graph[u].append(fwd)
        self.graph[v].append(rev)
        return fwd

    def build(self) -> None:
        """Materialize the residual graph with the lower-bound reduction."""
        balance = [Fraction(0)] * self._n
        for rec in self._records:
            rec.edge = self._add_residual_arc(
                rec.u, rec.v, rec.upper - rec.lower, rec.cost)
            balance[rec.u] -= rec.lower
            balance[rec.v] += rec.lower
        self._ss = self._n
        self._kk = self._n + 1
        self.graph.append([])
        self.graph.append([])
        self._demand_total = Fraction(0)
        for v in range(self._n):
            if balance[v] > 0:
                self._add_residual_arc(self._ss, v, balance[v], Fraction(0))
                self._demand_total += balance[v]
            elif balance[v] < 0:
                self._add_residual_arc(v, self._kk, -balance[v], Fraction(0))
        self._built = True

    def feasible(self) -> bool:
        """Find a feasible circulation (Edmonds-Karp); False if none exists."""
        if not self._built:
            self.build()
        ss, kk = self._ss, self._kk
        flow = Fraction(0)
        while flow < self._demand_total:
            parent = [None] * len(self.graph)
            parent[ss] = (ss, -1)
            queue = deque([ss])
            while queue and parent[kk] is None:
                u = queue.popleft()
                for i, e in enumerate(self.graph[u]):
                    if e.cap > 0 and parent[e.to] is None:
                        parent[e.to] = (u, i)
                        queue.append(e.to)
            if parent[kk] is None:
                return False
            bottleneck = None
            v = kk
            while v != ss:
                u, i = parent[v]
                e = self.graph[u][i]
                bottleneck = e.cap if bottleneck is None else min(bottleneck, e.cap)
                v = u
            v = kk
            while v != ss:
                u, i = parent[v]
                e = self.graph[u][i]
                e.cap -= bottleneck
                self.graph[e.to][e.rev].cap += bottleneck
                v = u
            flow += bottleneck
        return True

    def minimize(self, iteration_budget: int) -> tuple[bool, int]:
        """Cancel negative-cost cycles until optimal or budget exhausted.

        Returns (proven_optimal, iterations_used).
        """
        iterations = 0
        while True:
            cycle = self._find_negative_cycle()
            if cycle is None:
                return True, iterations
            if iterations >= iteration_budget:
                return False, iterations
            bottleneck = min(e.cap for e in cycle)
            for e in cycle:
                e.cap -= bottleneck
                self.graph[e.to][e.rev].cap += bottleneck
            iterations += 1

    def _find_negative_cycle(self):
        """Bellman-Ford negative-cycle detection; returns list of _Edge or None."""
        graph = self.graph
        n = len(graph)
        dist = [Fraction(0)] * n
        parent = [None] * n
        last = -1
        for _ in range(n):
            last = -1
            for u in range(n):
                du = dist[u]
                for i, e in enumerate(graph[u]):
                    if e.cap > 0 and dist[e.to] > du + e.cost:
                        dist[e.to] = du + e.cost
                        parent[e.to] = (u, i)
                        last = e.to
            if last == -1:
                return None
        # `last` was still relaxed on the n-th pass: walking n parents lands
        # on a negative-cost cycle.
        y = last
        for _ in range(n):
            y = parent[y][0]
        cycle = []
        cur = y
        while True:
            u, i = parent[cur]
            cycle.append(graph[u][i])
            cur = u
            if cur == y:
                break
        return cycle

    def flow_of(self, record_index: int) -> Fraction:
        """Flow on the arc added earlier (lower bound + used residual capacity)."""
        rec = self._records[record_index]
        return rec.upper - rec.edge.cap
