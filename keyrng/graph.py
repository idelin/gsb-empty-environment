"""A tiny dataflow graph executor where nodes own explicit random keys.

There is no global RNG state: each node's key is derived purely from the
root key and the node's name via ``fold_in(root, name)``.  Consequences:

* node randomness does not depend on execution order;
* a run can be resumed from a partial cache: cached nodes are not
  re-executed and therefore do not re-consume their keys;
* strict mode (KeyRegistry) raises if any key is consumed twice.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Callable, Iterable

from .prng import KeyRegistry, PRNGKey, fold_in


@dataclass(frozen=True)
class Node:
    """A graph node. ``op(key, inputs)`` produces the node's value."""

    name: str
    inputs: tuple[str, ...]
    op: Callable[[PRNGKey, list], Any]


def topo_sort(nodes: Iterable[Node]) -> list[Node]:
    """Kahn's algorithm; deterministic (ready nodes taken by name)."""
    by_name: dict[str, Node] = {}
    for node in nodes:
        if node.name in by_name:
            raise ValueError(f"duplicate node name: {node.name!r}")
        by_name[node.name] = node
    indegree = {name: 0 for name in by_name}
    for node in by_name.values():
        for dep in node.inputs:
            if dep not in by_name:
                raise ValueError(f"node {node.name!r} depends on unknown {dep!r}")
            indegree[node.name] += 1
    ready = sorted(n for n, d in indegree.items() if d == 0)
    order: list[Node] = []
    while ready:
        name = ready.pop(0)
        order.append(by_name[name])
        for other in by_name.values():
            if name in other.inputs:
                indegree[other.name] -= 1
                if indegree[other.name] == 0:
                    ready.append(other.name)
        ready.sort()
    if len(order) != len(by_name):
        raise ValueError("graph has a cycle")
    return order


class Executor:
    """Executes nodes, deriving one key per node from the root key."""

    def __init__(self, root_key: PRNGKey, strict: bool = False):
        self.root_key = root_key
        self.registry = KeyRegistry() if strict else None
        self.keys_consumed = 0

    def node_key(self, name: str) -> PRNGKey:
        """The key owned by a node; counted only when actually consumed."""
        k = fold_in(self.root_key, name)
        if self.registry is not None:
            self.registry.register(k)
        self.keys_consumed += 1
        return k

    def run(
        self, nodes: Iterable[Node], cache: dict[str, Any] | None = None
    ) -> dict[str, Any]:
        """Execute in topological order, skipping nodes already in ``cache``.

        Returns the cache mapping node name -> value.  Nodes found in the
        cache are not re-executed, so their keys are not consumed again.
        """
        results: dict[str, Any] = {} if cache is None else cache
        for node in topo_sort(nodes):
            if node.name in results:
                continue
            key = self.node_key(node.name)
            inputs = [results[dep] for dep in node.inputs]
            results[node.name] = node.op(key, inputs)
        return results
