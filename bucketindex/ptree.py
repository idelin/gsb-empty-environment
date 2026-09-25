"""Persistent count index over fine buckets (path-copying segment tree).

Every mutation returns a new root; previous roots stay valid forever, so
historic snapshots can be queried without ever rebuilding the tree.
Each update copies only the O(log N) nodes on the affected path.

Leaves hold the events of one fine bucket as an immutable tuple of
``(ticks, event_id, version)`` items sorted by ``(ticks, event_id)`` --
duplicate timestamps are ordered by the stable event id.  Internal nodes
carry only aggregate counts, which power range-count and k-th-event
(order statistics) queries.
"""

from __future__ import annotations

from bisect import bisect_left, insort
from dataclasses import dataclass
from typing import Optional, Tuple


# Bucket-index universe: [-2**40, 2**40).  Depth is 41, updates stay cheap.
LO = -(1 << 40)
HI = 1 << 40

Item = Tuple[int, str, int]  # (ticks, event_id, version)


@dataclass(frozen=True)
class Node:
    count: int
    left: Optional["Node"] = None
    right: Optional["Node"] = None
    items: Tuple[Item, ...] = ()  # non-empty only on leaves


def _count(node: Optional[Node]) -> int:
    return node.count if node is not None else 0


class PersistentCountTree:
    """Immutable-on-update segment tree; a root identifies a version."""

    def update(self, root: Optional[Node], index: int, item: Item, delta: int) -> Node:
        """Apply +1/-1 for ``item`` in leaf ``index``; returns the new root."""
        return self._update(root, LO, HI, index, item, delta)

    def _update(self, node, lo, hi, index, item, delta) -> Node:
        if hi - lo == 1:
            items = node.items if node is not None else ()
            if delta > 0:
                new_items = list(items)
                insort(new_items, item)
                items = tuple(new_items)
            else:
                items = tuple(x for x in items if x != item)
            return Node(_count(node) + delta, items=items)
        mid = (lo + hi) // 2
        left = node.left if node is not None else None
        right = node.right if node is not None else None
        if index < mid:
            left = self._update(left, lo, mid, index, item, delta)
        else:
            right = self._update(right, mid, hi, index, item, delta)
        return Node(_count(left) + _count(right), left, right)

    def count(self, root: Optional[Node], qlo: int, qhi: int) -> int:
        """Number of events in fine buckets [qlo, qhi)."""
        if root is None or qlo >= qhi:
            return 0
        return self._count(root, LO, HI, qlo, qhi)

    def _count(self, node, lo, hi, qlo, qhi) -> int:
        if node is None or qhi <= lo or hi <= qlo:
            return 0
        if qlo <= lo and hi <= qhi:
            return node.count
        mid = (lo + hi) // 2
        return self._count(node.left, lo, mid, qlo, qhi) + self._count(
            node.right, mid, hi, qlo, qhi
        )

    def leaf_items(self, root: Optional[Node], index: int) -> Tuple[Item, ...]:
        """Sorted items of one fine bucket (for edge itemisation)."""
        node, lo, hi = root, LO, HI
        while node is not None and hi - lo > 1:
            mid = (lo + hi) // 2
            if index < mid:
                node, hi = node.left, mid
            else:
                node, lo = node.right, mid
        return node.items if node is not None else ()

    def kth(self, root: Optional[Node], k: int) -> Item:
        """0-based k-th event in (ticks, event_id) order."""
        if root is None or not 0 <= k < root.count:
            raise IndexError(f"k={k} out of range")
        node, lo, hi = root, LO, HI
        while hi - lo > 1:
            mid = (lo + hi) // 2
            left_count = _count(node.left)
            if k < left_count:
                node, hi = node.left, mid
            else:
                k -= left_count
                node, lo = node.right, mid
        return node.items[k]
