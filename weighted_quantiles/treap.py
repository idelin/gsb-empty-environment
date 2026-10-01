"""Randomized treap maintaining exact rational weights per distinct value.

Each node aggregates the weights of all observations that share the same
value (duplicate values are merged).  Every node caches the total weight of
its subtree, so cumulative-weight queries (quantile selection, weighted
ECDF) descend a single root-to-leaf path in O(log n) expected node visits
instead of re-sorting all samples.

The pseudo-random priorities come from a caller-injected ``random.Random``
instance, keeping the structure deterministic for a given seed.
"""

from __future__ import annotations

from fractions import Fraction
from typing import Any, Iterator, Optional, Tuple


class _Node:
    __slots__ = ("key", "priority", "weight", "total", "left", "right")

    def __init__(self, key: Any, priority: float, weight: Fraction) -> None:
        self.key = key
        self.priority = priority
        self.weight = weight  # weight of this value alone
        self.total = weight   # weight of the whole subtree
        self.left: Optional[_Node] = None
        self.right: Optional[_Node] = None


def _total(node: Optional[_Node]) -> Fraction:
    return node.total if node is not None else Fraction(0)


class WeightedTreap:
    """Treap keyed by observation value with cumulative weight maintenance."""

    def __init__(self, rng) -> None:
        self._rng = rng
        self._root: Optional[_Node] = None
        self._size = 0
        self.visits = 0  # node accesses since the last reset (instrumentation)

    # ------------------------------------------------------------------
    # instrumentation
    # ------------------------------------------------------------------
    def reset_stats(self) -> None:
        self.visits = 0

    # ------------------------------------------------------------------
    # structural helpers
    # ------------------------------------------------------------------
    def _pull(self, node: _Node) -> None:
        node.total = node.weight + _total(node.left) + _total(node.right)

    def _rotate_right(self, node: _Node) -> _Node:
        pivot = node.left
        node.left = pivot.right
        pivot.right = node
        self._pull(node)
        self._pull(pivot)
        return pivot

    def _rotate_left(self, node: _Node) -> _Node:
        pivot = node.right
        node.right = pivot.left
        pivot.left = node
        self._pull(node)
        self._pull(pivot)
        return pivot

    # ------------------------------------------------------------------
    # mutation
    # ------------------------------------------------------------------
    def add(self, key: Any, delta: Fraction) -> None:
        """Add ``delta`` (may be negative) to the weight stored at ``key``.

        A node whose weight drops to exactly zero is removed (zero-weight
        elimination).  Dropping below zero raises ``KeyError``.
        """
        if delta == 0:
            return
        self._root = self._add(self._root, key, delta)

    def _add(self, node: Optional[_Node], key: Any, delta: Fraction) -> _Node:
        self.visits += 1
        if node is None:
            if delta < 0:
                raise KeyError(key)
            self._size += 1
            return _Node(key, self._rng.random(), delta)
        if key < node.key:
            node.left = self._add(node.left, key, delta)
            if node.left is not None and node.left.priority < node.priority:
                node = self._rotate_right(node)
            else:
                self._pull(node)
        elif key > node.key:
            node.right = self._add(node.right, key, delta)
            if node.right is not None and node.right.priority < node.priority:
                node = self._rotate_left(node)
            else:
                self._pull(node)
        else:
            new_weight = node.weight + delta
            if new_weight < 0:
                raise KeyError(key)
            if new_weight == 0:
                self._size -= 1
                return self._merge_children(node.left, node.right)
            node.weight = new_weight
            self._pull(node)
        return node

    def _merge_children(self, left: Optional[_Node], right: Optional[_Node]) -> Optional[_Node]:
        if left is None:
            return right
        if right is None:
            return left
        self.visits += 1
        if left.priority < right.priority:
            left.right = self._merge_children(left.right, right)
            self._pull(left)
            return left
        right.left = self._merge_children(left, right.left)
        self._pull(right)
        return right

    # ------------------------------------------------------------------
    # queries
    # ------------------------------------------------------------------
    @property
    def size(self) -> int:
        return self._size

    def total_weight(self) -> Fraction:
        return _total(self._root)

    def minimum(self) -> Any:
        node = self._root
        if node is None:
            raise ValueError("minimum of empty structure")
        while node.left is not None:
            self.visits += 1
            node = node.left
        self.visits += 1
        return node.key

    def maximum(self) -> Any:
        node = self._root
        if node is None:
            raise ValueError("maximum of empty structure")
        while node.right is not None:
            self.visits += 1
            node = node.right
        self.visits += 1
        return node.key

    def quantile_interval(self, q: Fraction) -> Tuple[Any, Any]:
        """Exact weighted quantile for ``q`` in [0, 1].

        Returns the value interval ``(lo, hi)`` such that every x in
        [lo, hi] satisfies F(x-) <= q <= F(x) for the weighted ECDF F.
        ``lo == hi`` for a unique answer; ``lo < hi`` when q coincides
        exactly with a cumulative-weight jump boundary (discrete jump
        point), in which case any value between the two adjacent observed
        values is a valid q-quantile.  q=0 / q=1 return the minimum /
        maximum positive-weight value.
        """
        if not Fraction(0) <= q <= Fraction(1):
            raise ValueError("q must lie in [0, 1]")
        total = self.total_weight()
        if total == 0:
            raise ValueError("quantile of empty structure")
        if q == 0:
            key = self.minimum()
            return (key, key)
        target = q * total  # exact rational arithmetic
        acc = Fraction(0)   # weight strictly before the current subtree
        succ = None         # smallest key greater than the current node
        node = self._root
        while True:
            self.visits += 1
            prefix = acc + _total(node.left)
            cum = prefix + node.weight
            if target <= prefix:
                succ = node.key
                node = node.left
                continue
            if target > cum:
                acc = cum
                node = node.right
                continue
            if target < cum:
                return (node.key, node.key)
            # target == cum: q sits exactly on a jump boundary.
            upper = self._successor_key(node, succ)
            if upper is None:
                return (node.key, node.key)  # q == 1, node is the maximum
            return (node.key, upper)

    def _successor_key(self, node: _Node, ancestor_succ: Optional[Any]) -> Optional[Any]:
        """Smallest key greater than ``node.key``.

        ``ancestor_succ`` is the closest greater key seen on the descent
        path (the last ancestor from which we went left).
        """
        child = node.right
        if child is None:
            return ancestor_succ
        self.visits += 1
        while child.left is not None:
            self.visits += 1
            child = child.left
        return child.key
    def weight_le(self, x: Any) -> Fraction:
        """Total weight of observations with value <= x (unnormalized ECDF)."""
        acc = Fraction(0)
        node = self._root
        while node is not None:
            self.visits += 1
            if x < node.key:
                node = node.left
            else:
                acc += _total(node.left) + node.weight
                node = node.right
        return acc

    def items(self) -> Iterator[Tuple[Any, Fraction]]:
        """Yield ``(value, weight)`` pairs in ascending value order."""
        stack = []
        node = self._root
        while stack or node is not None:
            while node is not None:
                stack.append(node)
                node = node.left
            node = stack.pop()
            yield node.key, node.weight
            node = node.right
