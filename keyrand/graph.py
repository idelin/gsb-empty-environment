"""计算图：节点持有随机键而非全局状态；支持分块执行、断点续跑、严格键冲突检测。"""
from __future__ import annotations

import hashlib
from typing import Callable, Iterable

from .prng import Key, fold_in


class KeyConflictError(RuntimeError):
    """严格模式下同一键被多个节点占用时抛出。"""


class Node:
    def __init__(self, name: str, fn: Callable, inputs: tuple[str, ...], key: Key):
        self.name = name
        self.fn = fn          # fn(key, *input_values) -> value
        self.inputs = inputs
        self.key = key
        self.compute_count = 0  # 键被“消耗”（节点实际求值）的次数

    def compute(self, values: list):
        self.compute_count += 1
        return self.fn(self.key, *values)


class Graph:
    def __init__(self, root: Key, strict: bool = False):
        self.root = root
        self.strict = strict
        self.nodes: dict[str, Node] = {}
        self._key_owners: dict[Key, str] = {}

    def _name_key(self, name: str) -> Key:
        digest = int.from_bytes(hashlib.sha256(name.encode()).digest()[:8], "big")
        return fold_in(self.root, digest)

    def add_node(
        self, name: str, fn: Callable, inputs: Iterable[str] = (), key: Key | None = None
    ) -> Node:
        if name in self.nodes:
            raise ValueError(f"节点名重复: {name}")
        key = key if key is not None else self._name_key(name)
        owner = self._key_owners.get(key)
        if owner is not None and self.strict:
            raise KeyConflictError(
                f"键冲突: 节点 {name!r} 与 {owner!r} 使用了同一个键 {key!r}"
            )
        self._key_owners.setdefault(key, name)
        node = Node(name, fn, tuple(inputs), key)
        self.nodes[name] = node
        return node


class Executor:
    """拓扑序执行；cache 中已完成的节点不会重复求值（不重复消耗键）。"""

    def __init__(self, graph: Graph):
        self.graph = graph
        self.cache: dict[str, object] = {}

    def _topo_order(self, outputs: Iterable[str]) -> list[str]:
        order: list[str] = []
        visited: set[str] = set()

        def visit(name: str):
            if name in visited:
                return
            visited.add(name)
            node = self.graph.nodes[name]
            for dep in node.inputs:
                visit(dep)
            order.append(name)

        for out in outputs:
            visit(out)
        return order

    def run(
        self,
        outputs: Iterable[str],
        chunk_size: int | None = None,
        max_nodes: int | None = None,
    ) -> dict[str, object]:
        """执行到产出 ``outputs``。

        - ``chunk_size``: 分块执行，仅影响调度，不影响结果。
        - ``max_nodes``: 最多新求值多少个节点后中断（模拟部分完成），
          之后再次调用 run 即可从 cache 续跑。
        """
        order = self._topo_order(outputs)
        step = chunk_size if chunk_size else len(order) or 1
        done = 0
        for start in range(0, len(order), step):
            for name in order[start : start + step]:
                if name in self.cache:
                    continue
                if max_nodes is not None and done >= max_nodes:
                    return {o: self.cache.get(o) for o in outputs}
                node = self.graph.nodes[name]
                values = [self.cache[dep] for dep in node.inputs]
                self.cache[name] = node.compute(values)
                done += 1
        return {o: self.cache[o] for o in outputs}
