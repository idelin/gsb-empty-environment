"""Persistent (path-copying) treap with subtree count/sum augmentation.

Every mutation returns a new root; old roots stay valid and share structure
with the new tree, so a single update costs O(log n) new nodes instead of
rebuilding the whole tree. Priorities are derived deterministically from the
key so results are reproducible.
"""

from __future__ import annotations

import hashlib
from dataclasses import dataclass
from typing import Any, Iterator, Optional, Tuple


def _priority(key: Any) -> int:
    digest = hashlib.blake2b(repr(key).encode("utf-8"), digest_size=8)
    return int.from_bytes(digest.digest(), "big")


@dataclass(frozen=True)
class Node:
    key: Any
    prio: int
    value: int = 1            # numeric, folded into subtree total
    payload: Any = None       # arbitrary, not aggregated
    left: Optional["Node"] = None
    right: Optional["Node"] = None
    count: int = 1            # subtree size
    total: int = 1            # subtree sum of values


def _make(key: Any, prio: int, value: int, payload: Any,
          left: Optional[Node], right: Optional[Node]) -> Node:
    count = 1 + (left.count if left else 0) + (right.count if right else 0)
    total = value + (left.total if left else 0) + (right.total if right else 0)
    return Node(key, prio, value, payload, left, right, count, total)


def _count(node: Optional[Node]) -> int:
    return node.count if node else 0


def split(root: Optional[Node], key: Any) -> Tuple[Optional[Node], Optional[Node]]:
    """Split into (< key, >= key)."""
    if root is None:
        return None, None
    if key <= root.key:
        left, right = split(root.left, key)
        return left, _make(root.key, root.prio, root.value, root.payload, right, root.right)
    left, right = split(root.right, key)
    return _make(root.key, root.prio, root.value, root.payload, root.left, left), right


def merge(left: Optional[Node], right: Optional[Node]) -> Optional[Node]:
    if left is None:
        return right
    if right is None:
        return left
    if left.prio >= right.prio:
        return _make(left.key, left.prio, left.value, left.payload,
                     left.left, merge(left.right, right))
    return _make(right.key, right.prio, right.value, right.payload,
                 merge(left, right.left), right.right)


def insert(root: Optional[Node], key: Any, value: int = 1,
           payload: Any = None, prio: Optional[int] = None) -> Node:
    """Insert or replace (key, value, payload)."""
    if prio is None:
        prio = _priority(key)
    if root is None:
        return Node(key, prio, value, payload)
    if key == root.key:
        return _make(key, prio, value, payload, root.left, root.right)
    if prio > root.prio:
        left, right = split(root, key)
        return _make(key, prio, value, payload, left, right)
    if key < root.key:
        return _make(root.key, root.prio, root.value, root.payload,
                     insert(root.left, key, value, payload, prio), root.right)
    return _make(root.key, root.prio, root.value, root.payload,
                 root.left, insert(root.right, key, value, payload, prio))


def erase(root: Optional[Node], key: Any) -> Optional[Node]:
    if root is None:
        return None
    if key == root.key:
        return merge(root.left, root.right)
    if key < root.key:
        return _make(root.key, root.prio, root.value, root.payload,
                     erase(root.left, key), root.right)
    return _make(root.key, root.prio, root.value, root.payload,
                 root.left, erase(root.right, key))


def get(root: Optional[Node], key: Any) -> Optional[Node]:
    node = root
    while node is not None:
        if key == node.key:
            return node
        node = node.left if key < node.key else node.right
    return None


def size(root: Optional[Node]) -> int:
    return _count(root)


def range_total(root: Optional[Node], lo: Any = None, hi: Any = None) -> int:
    """Sum of values for keys in [lo, hi); None means unbounded."""
    if root is None:
        return 0
    if lo is None and hi is None:
        return root.total
    if lo is not None and root.key < lo:
        return range_total(root.right, lo, hi)
    if hi is not None and root.key >= hi:
        return range_total(root.left, lo, hi)
    return (root.value
            + range_total(root.left, lo, hi)
            + range_total(root.right, lo, hi))


def select(root: Optional[Node], k: int) -> Node:
    """Return the k-th node (0-based) in key order."""
    if root is None or k < 0 or k >= root.count:
        raise IndexError(f"select index {k} out of range")
    left_count = _count(root.left)
    if k < left_count:
        return select(root.left, k)
    if k == left_count:
        return root
    return select(root.right, k - left_count - 1)


def iterate(root: Optional[Node], lo: Any = None, hi: Any = None
            ) -> Iterator[Tuple[Any, int, Any]]:
    """Yield (key, value, payload) for keys in [lo, hi), in key order."""
    if root is None:
        return
    if lo is None or root.key >= lo:
        yield from iterate(root.left, lo, hi)
    if (lo is None or root.key >= lo) and (hi is None or root.key < hi):
        yield root.key, root.value, root.payload
    if hi is None or root.key < hi:
        yield from iterate(root.right, lo, hi)
