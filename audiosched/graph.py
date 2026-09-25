"""Directed routing graph with cycle detection and topological ordering."""
from __future__ import annotations


class RoutingGraph:
    def __init__(self, nodes):
        self.nodes = list(nodes)
        self.edges = {n: set() for n in self.nodes}

    def copy(self) -> "RoutingGraph":
        g = RoutingGraph(self.nodes)
        for n, dsts in self.edges.items():
            g.edges[n] = set(dsts)
        return g

    def connect(self, src, dst):
        if src not in self.edges or dst not in self.edges:
            raise KeyError(f"unknown node: {src!r} or {dst!r}")
        self.edges[src].add(dst)

    def disconnect(self, src, dst):
        if src not in self.edges or dst not in self.edges:
            raise KeyError(f"unknown node: {src!r} or {dst!r}")
        self.edges[src].discard(dst)

    def topo_order(self):
        """Kahn's algorithm; deterministic (node declaration order)."""
        indeg = {n: 0 for n in self.nodes}
        for s in self.nodes:
            for d in self.edges[s]:
                indeg[d] += 1
        ready = [n for n in self.nodes if indeg[n] == 0]
        order = []
        while ready:
            n = ready.pop(0)
            order.append(n)
            for d in sorted(self.edges[n]):
                indeg[d] -= 1
                if indeg[d] == 0:
                    ready.append(d)
        if len(order) != len(self.nodes):
            raise ValueError("routing graph contains a cycle")
        return order

    def has_cycle(self) -> bool:
        try:
            self.topo_order()
        except ValueError:
            return True
        return False
