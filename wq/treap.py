"""Randomized treap (balanced BST) with cumulative subtree weights.

Each node stores an exact rational (Fraction) weight and maintains the
aggregate weight of its subtree, so cumulative-weight queries and
order-statistic selection run in O(log n) without ever re-sorting.

Priorities come from a caller-injected random source (anything exposing
``getrandbits``), which makes the structure reproducible.  Every node
access is counted in ``visits`` so callers can report how much of the
tree an operation actually touched.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import Any, Optional


@dataclass
class _Node:
    key: Any
    weight: Fraction
    priority: int
    left: Optional["_Node"] = None
    right: Optional["_Node"] = None
    total: Fraction = field(default=Fraction(0))
    count: int = 0


def _total(node: Optional[_Node]) -> Fraction:
    return node.total if node is not None else Fraction(0)


def _count(node: Optional[_Node]) -> int:
    return node.count if node is not None else 0


def _pull(node: _Node) -> None:
    node.total = _total(node.left) + node.weight + _total(node.right)
    node.count = _count(node.left) + 1 + _count(node.right)


class Treap:
    """Ordered map from key to positive rational aggregate weight."""

    def __init__(self, rng) -> None:
        self._rng = rng
        self.root: Optional[_Node] = None
        self.visits = 0  # instrumentation: number of node visits

    # -- mutation ----------------------------------------------------
    def adjust(self, key: Any, delta: Fraction) -> bool:
        """Add ``delta`` to the weight at ``key``.

        Creates the node when missing; deletes it when its weight
        reaches zero.  Returns True when a new node was created.
        """
        self.root, created = self._adjust(self.root, key, delta)
        return created

    def _adjust(self, node: Optional[_Node], key: Any, delta: Fraction):
        self.visits += 1
        if node is None:
            new = _Node(key=key, weight=delta, priority=self._rng.getrandbits(64))
            _pull(new)
            return new, True
        if key == node.key:
            node.weight += delta
            if node.weight == 0:
                return self._merge(node.left, node.right), False
            _pull(node)
            return node, False
        if key < node.key:
            node.left, created = self._adjust(node.left, key, delta)
            _pull(node)
            return self._rot_right_if_needed(node), created
        node.right, created = self._adjust(node.right, key, delta)
        _pull(node)
        return self._rot_left_if_needed(node), created

    @staticmethod
    def _rot_right_if_needed(node: _Node) -> _Node:
        child = node.left
        if child is not None and child.priority < node.priority:
            node.left = child.right
            child.right = node
            _pull(node)
            _pull(child)
            return child
        return node

    @staticmethod
    def _rot_left_if_needed(node: _Node) -> _Node:
        child = node.right
        if child is not None and child.priority < node.priority:
            node.right = child.left
            child.left = node
            _pull(node)
            _pull(child)
            return child
        return node

    def _merge(self, a: Optional[_Node], b: Optional[_Node]) -> Optional[_Node]:
        if a is None:
            return b
        if b is None:
            return a
        self.visits += 1
        if a.priority < b.priority:
            a.right = self._merge(a.right, b)
            _pull(a)
            return a
        b.left = self._merge(a, b.left)
        _pull(b)
        return b
