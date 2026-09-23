"""Balanced interval tree (randomized treap) maintaining an ordered,
non-commutative fold of 2x2 matrices per subtree.

Each node's aggregate is the in-order product
    agg(node) = agg(left) * value(node) * agg(right)   (mod m)
so the root aggregate equals the fold of the whole sequence in key order.
Splits/merges keep the treap balanced with high probability (O(log n)).
"""

from __future__ import annotations

import hashlib
from dataclasses import dataclass

from matrix import IDENTITY, Matrix, mul


def _priority(key: tuple) -> int:
    """Deterministic pseudo-random priority (reproducible across runs)."""
    digest = hashlib.sha256(repr(key).encode("utf-8")).digest()
    return int.from_bytes(digest[:8], "big")


@dataclass
class Node:
    key: tuple
    value: Matrix
    priority: int
    left: "Node | None" = None
    right: "Node | None" = None
    agg: Matrix = IDENTITY


class Treap:
    def __init__(self, mod: int):
        self.mod = mod
        self.root: Node | None = None
        self.size = 0

    def _recalc(self, node: Node | None) -> None:
        if node is None:
            return
        left_agg = node.left.agg if node.left else IDENTITY
        right_agg = node.right.agg if node.right else IDENTITY
        node.agg = mul(mul(left_agg, node.value, self.mod), right_agg, self.mod)

    def _merge(self, left: Node | None, right: Node | None) -> Node | None:
        if left is None:
            return right
        if right is None:
            return left
        if left.priority < right.priority:
            left.right = self._merge(left.right, right)
            self._recalc(left)
            return left
        right.left = self._merge(left, right.left)
        self._recalc(right)
        return right

    def _split(self, node: Node | None, key: tuple):
        """Split into (keys < key, keys >= key)."""
        if node is None:
            return None, None
        if node.key < key:
            lo, hi = self._split(node.right, key)
            node.right = lo
            self._recalc(node)
            return node, hi
        lo, hi = self._split(node.left, key)
        node.left = hi
        self._recalc(node)
        return lo, node

    def insert(self, key: tuple, value: Matrix) -> None:
        node = Node(key=key, value=value, priority=_priority(key))
        self._recalc(node)
        lo, hi = self._split(self.root, key)
        self.root = self._merge(self._merge(lo, node), hi)
        self.size += 1

    def delete(self, key: tuple) -> bool:
        # key + (None,) is the smallest tuple strictly greater than key,
        # because a 2-tuple prefix compares less than any 3-tuple extension.
        lo, ge = self._split(self.root, key)
        mid, hi = self._split(ge, key + (None,))
        if mid is None:
            self.root = lo
            return False
        self.root = self._merge(lo, hi)
        self.size -= 1
        return True

    def aggregate(self) -> Matrix:
        return self.root.agg if self.root else IDENTITY

    def inorder(self):
        def walk(node):
            if node is None:
                return
            yield from walk(node.left)
            yield node.key, node.value
            yield from walk(node.right)

        return list(walk(self.root))
