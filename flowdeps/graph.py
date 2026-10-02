"""Dependency graph over step runs, with edge retraction and cycle diagnosis."""
from __future__ import annotations

from typing import Optional

from .model import StepDep


class CycleError(Exception):
    """Raised when an illegal cyclic processing chain is detected."""

    def __init__(self, chain):
        self.chain = [str(c) for c in chain]
        super().__init__(
            "illegal cyclic processing chain: " + " -> ".join(self.chain)
        )


class DepGraph:
    """step id -> set of dependency edges recorded by its latest run."""

    def __init__(self):
        self.step_deps: dict[str, frozenset] = {}

    def install(self, step: str, deps) -> None:
        """Install a step's new edges, retracting its old ones."""
        self.step_deps[step] = frozenset(deps)

    def retract(self, step: str) -> None:
        self.step_deps.pop(step, None)

    def consumers_of_step(self, step: str) -> set:
        return {
            s
            for s, deps in self.step_deps.items()
            if any(isinstance(d, StepDep) and d.step == step for d in deps)
        }

    def _producer_map(self) -> dict:
        return {
            s: sorted(d.step for d in deps if isinstance(d, StepDep))
            for s, deps in self.step_deps.items()
        }

    def find_cycle(self) -> Optional[list]:
        producers = self._producer_map()
        color: dict[str, int] = {}

        def dfs(node: str, path: list) -> Optional[list]:
            color[node] = 1
            path.append(node)
            for nxt in producers.get(node, []):
                if color.get(nxt) == 1:
                    return path[path.index(nxt):] + [nxt]
                if color.get(nxt) is None:
                    found = dfs(nxt, path)
                    if found:
                        return found
            path.pop()
            color[node] = 2
            return None

        for step in sorted(producers):
            if color.get(step) is None:
                found = dfs(step, [])
                if found:
                    return found
        return None

    def topo_order(self) -> list:
        """Producers before consumers.  Raises CycleError if cyclic."""
        producers = self._producer_map()
        consumers: dict[str, set] = {s: set() for s in producers}
        for step, deps in self.step_deps.items():
            for d in deps:
                if isinstance(d, StepDep):
                    producers.setdefault(d.step, [])
                    consumers.setdefault(d.step, set()).add(step)
        indegree = {s: len(p) for s, p in producers.items()}
        ready = sorted(s for s, deg in indegree.items() if deg == 0)
        order: list[str] = []
        while ready:
            node = ready.pop(0)
            order.append(node)
            for consumer in sorted(consumers.get(node, ())):
                indegree[consumer] -= 1
                if indegree[consumer] == 0:
                    ready.append(consumer)
            ready.sort()
        if len(order) != len(producers):
            raise CycleError(self.find_cycle() or ["<unknown>"])
        return order
