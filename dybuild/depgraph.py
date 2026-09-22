"""Dependency graph over node names and ordered cycle evidence.

Edges are "depends-on" edges: an edge A -> B means node A reads a file that
node B produces (statically declared or discovered at runtime).
"""
from __future__ import annotations

from collections import defaultdict
from typing import Iterable


class CycleError(Exception):
    def __init__(self, cycle: list[str]):
        self.cycle = cycle
        super().__init__("dependency cycle: " + " -> ".join(cycle))


class DependencyGraph:
    def __init__(self) -> None:
        self._edges: dict[str, set[str]] = defaultdict(set)
        self.nodes: set[str] = set()

    def add_node(self, node: str) -> None:
        self.nodes.add(node)

    def add_edge(self, src: str, dst: str) -> bool:
        self.nodes.update((src, dst))
        if dst in self._edges[src]:
            return False
        self._edges[src].add(dst)
        return True

    def edges_of(self, node: str) -> set[str]:
        return set(self._edges.get(node, ()))

    def apply_candidate(
        self, node: str, new_deps: Iterable[str]
    ) -> "CycleError | None":
        """Atomically accept *new_deps* for *node*, or reject with cycle proof.

        The candidate is first verified; on a cycle nothing is installed.
        """
        new_deps = set(new_deps)
        previous = set(self._edges.get(node, ()))
        trial = DependencyGraph()
        for n, deps in self._edges.items():
            for d in deps:
                if n != node:
                    trial.add_edge(n, d)
        for d in new_deps:
            trial.add_edge(node, d)
        for n in self.nodes | {node} | new_deps:
            trial.add_node(n)
        cyc = trial.find_cycle()
        if cyc is not None:
            return CycleError(cyc)
        # commit inside the in-memory graph object
        self.nodes |= trial.nodes
        if node in self._edges:
            del self._edges[node]
        for d in new_deps:
            self._edges[node].add(d)
        # ensure deps removed by the new observation set are gone (handled above)
        for removed in previous - new_deps:
            self._edges[node].discard(removed)
        return None

    def find_cycle(self, start: str | None = None) -> list[str] | None:
        """DFS cycle detection, returning an ordered, rotation-normalized cycle.

        A self-cycle is returned as [node, node].
        """
        WHITE, GRAY, BLACK = 0, 1, 2
        color: dict[str, int] = {n: WHITE for n in self.nodes}
        stack: list[str] = []
        pos: dict[str, int] = {}

        def visit(u: str) -> list[str] | None:
            color[u] = GRAY
            pos[u] = len(stack)
            stack.append(u)
            for v in sorted(self._edges.get(u, ())):
                if v not in color:
                    color[v] = WHITE
                if color[v] == GRAY:
                    cyc = stack[pos[v]:] + [v]
                    return cyc
                if color[v] == WHITE:
                    found = visit(v)
                    if found is not None:
                        return found
            stack.pop()
            pos.pop(u, None)
            color[u] = BLACK
            return None

        roots = sorted(self.nodes) if start is None else [start]
        for r in roots:
            if color.get(r, WHITE) == WHITE:
                found = visit(r)
                if found is not None:
                    return found
        return None
