"""Weighted string dictionary over a compressed radix tree.

Ordering for top-k queries: weight descending, then key ascending by
Unicode code point (Python ``str`` default ordering).

Weight updates and deletions perform O(key length) structural edits
only; they never sort the dictionary.  Top-k queries locate the prefix
in O(prefix length), then keep the best ``k`` items seen so far with a
size-bounded min-heap -- O(m log k) work and O(k) heap space for the
``m`` terminals below the prefix, with no full sort of the subtree.
"""

from __future__ import annotations

import heapq
from typing import Iterable, Optional

from . import radix


class InvariantError(AssertionError):
    pass


class _HeapItem:
    """Min-heap entry: ordered by (weight asc, key desc).

    The heap root is therefore the *worst* item among the current top-k,
    which lets a streaming scan evict it in O(log k).
    """

    __slots__ = ("key", "weight")

    def __init__(self, key: str, weight: int) -> None:
        self.key = key
        self.weight = weight

    def __lt__(self, other: "_HeapItem") -> bool:
        if self.weight != other.weight:
            return self.weight < other.weight
        return self.key > other.key


class PrefixDictionary:
    def __init__(self) -> None:
        self._root = radix.RadixNode()

    def __len__(self) -> int:
        return self._root.size

    def __contains__(self, key: str) -> bool:
        return radix.get(self._root, key) is not None

    def insert(self, key: str, weight: int) -> None:
        if not isinstance(key, str):
            raise TypeError("key must be a str")
        if not isinstance(weight, int) or isinstance(weight, bool):
            raise TypeError("weight must be an int")
        radix.insert(self._root, key, weight)

    # Convenience alias: insertion with an existing key overwrites weight.
    upsert = insert

    def delete(self, key: str) -> bool:
        if not isinstance(key, str):
            raise TypeError("key must be a str")
        return radix.delete(self._root, key)

    def weight_of(self, key: str) -> Optional[int]:
        return radix.get(self._root, key)

    def prefix_topk(self, prefix: str, k: int) -> list[tuple[str, int]]:
        """Up to ``k`` ``(key, weight)`` pairs under ``prefix``.

        Empty prefix means the whole dictionary.  ``k`` must be >= 0.
        """
        if not isinstance(prefix, str):
            raise TypeError("prefix must be a str")
        if not isinstance(k, int) or isinstance(k, bool) or k < 0:
            raise ValueError("k must be a non-negative int")
        located = radix.locate(self._root, prefix)
        if located is None:
            return []
        node, path = located
        heap: list[_HeapItem] = []
        for full_key, weight in radix.iter_subtree(node, path):
            item = _HeapItem(full_key, weight)
            if len(heap) < k:
                heapq.heappush(heap, item)
            elif item > heap[0]:
                heapq.heapreplace(heap, item)
        ordered = sorted(heap, key=lambda it: (-it.weight, it.key))
        return [(it.key, it.weight) for it in ordered]

    def items(self) -> list[tuple[str, int]]:
        return list(radix.iter_subtree(self._root, ""))

    # -- structural invariants -------------------------------------------

    def check_invariants(self) -> None:
        """Raise :class:`InvariantError` if any structure invariant fails.

        Invariants checked:
        1. root edge is the empty string.
        2. every child dict key equals the first character of its edge.
        3. sibling edges share no common non-empty prefix.
        4. every non-root terminal-less node has >= 2 children.
        5. edges are non-empty.
        6. subtree ``size`` equals the number of terminals beneath.
        7. reconstructed full keys are distinct.
        8. every weight is an int (booleans rejected on insert).
        """
        root = self._root
        if root.edge != "":
            raise InvariantError("root edge must be empty")
        seen: set[str] = set()
        self._check_node(root, "", seen, is_root=True)

    def _check_node(
        self,
        node: radix.RadixNode,
        path: str,
        seen: set[str],
        is_root: bool,
    ) -> int:
        if not is_root and not node.edge:
            raise InvariantError(f"empty edge at {path!r}")
        if node.weight is not None:
            if isinstance(node.weight, bool) or not isinstance(node.weight, int):
                raise InvariantError(f"non-int weight at {path!r}")
            if path in seen:
                raise InvariantError(f"duplicate key {path!r}")
            seen.add(path)
        if (
            not is_root
            and node.weight is None
            and len(node.children) < 2
        ):
            raise InvariantError(
                f"uncompressed unary/leaf routing node at {path!r}"
            )
        edges = list(node.children)
        for i, edge_i in enumerate(edges):
            if node.children[edge_i].edge[0] != edge_i:
                raise InvariantError(
                    f"child map key {edge_i!r} does not index edge "
                    f"{node.children[edge_i].edge!r} at {path!r}"
                )
            for edge_j in edges[i + 1 :]:
                if edge_i == edge_j or edge_i[0] == edge_j[0]:
                    raise InvariantError(
                        f"sibling edges share first char at {path!r}: "
                        f"{edge_i!r} vs {edge_j!r}"
                    )
        counted = 1 if node.weight is not None else 0
        for child in node.children.values():
            counted += self._check_node(child, path + child.edge, seen, False)
        if counted != node.size:
            raise InvariantError(
                f"stale size at {path!r}: cached {node.size}, actual {counted}"
            )
        return counted
