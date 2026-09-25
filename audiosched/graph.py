"""Directed routing graph with deterministic cycle detection."""

from __future__ import annotations


class Graph:
    """Directed graph over node names; edges are (src, dst) pairs."""

    def __init__(self, nodes=()):
        self.nodes = set(nodes)
        self.edges: set[tuple[str, str]] = set()

    def add_edge(self, src: str, dst: str) -> None:
        if src not in self.nodes or dst not in self.nodes:
            raise KeyError(f"unknown node in edge {src!r}->{dst!r}")
        self.edges.add((src, dst))

    def remove_edge(self, src: str, dst: str) -> None:
        self.edges.discard((src, dst))

    def has_edge(self, src: str, dst: str) -> bool:
        return (src, dst) in self.edges

    def successors(self, node: str) -> list[str]:
        return sorted(d for s, d in self.edges if s == node)

    def copy(self) -> "Graph":
        g = Graph(self.nodes)
        g.edges = set(self.edges)
        return g

    def topological_order(self) -> list[str]:
        """Kahn's algorithm with deterministic (sorted) tie-breaking.

        Raises:
            ValueError: if the graph contains a cycle.
        """
        indegree = {n: 0 for n in self.nodes}
        for s, d in self.edges:
            indegree[d] += 1
        ready = sorted(n for n in self.nodes if indegree[n] == 0)
        order: list[str] = []
        while ready:
            node = ready.pop(0)
            order.append(node)
            for succ in self.successors(node):
                indegree[succ] -= 1
                if indegree[succ] == 0:
                    ready.append(succ)
            ready.sort()
        if len(order) != len(self.nodes):
            raise ValueError("routing graph contains a cycle")
        return order

    def has_cycle(self) -> bool:
        try:
            self.topological_order()
        except ValueError:
            return True
        return False
