"""One fold group: an ordered sequence of matrices with a segment tree.

Elements are ordered by a stable sort key ``(sort_key, table, pk)`` so that
equal sort keys keep a deterministic, reproducible order. The segment tree
mirrors that order and yields the same product as a left-to-right scan.
"""

from __future__ import annotations

from typing import Hashable

from .matrix import IDENTITY, Matrix, multiply, normalize
from .segment_tree import SegmentTree


class FoldGroup:
    def __init__(self, mod: int):
        self.mod = mod
        self._entries: dict[Hashable, tuple[float, Matrix]] = {}
        self._order: list[Hashable] = []
        self._tree = SegmentTree([], mod)

    def __len__(self) -> int:
        return len(self._entries)

    def _sort_key(self, pk: Hashable):
        sort_key, _ = self._entries[pk]
        return (sort_key, *pk) if isinstance(pk, tuple) else (sort_key, pk)

    def _rebuild(self) -> None:
        self._order = sorted(self._entries, key=self._sort_key)
        self._tree = SegmentTree(
            [self._entries[pk][1] for pk in self._order], self.mod
        )

    def insert(self, pk: Hashable, sort_key: float, matrix: Matrix) -> None:
        if pk in self._entries:
            raise KeyError(f"duplicate key {pk!r} in group")
        self._entries[pk] = (sort_key, normalize(matrix, self.mod))
        self._rebuild()

    def update(self, pk: Hashable, sort_key: float, matrix: Matrix) -> None:
        old_sort_key, _ = self._entries[pk]  # raises KeyError if missing
        matrix = normalize(matrix, self.mod)
        self._entries[pk] = (sort_key, matrix)
        if sort_key == old_sort_key:
            # Position unchanged: O(log n) point update on the tree.
            self._tree.set(self._order.index(pk), matrix)
        else:
            self._rebuild()

    def delete(self, pk: Hashable) -> None:
        del self._entries[pk]  # raises KeyError if missing
        self._rebuild()

    def product(self) -> Matrix:
        """Incrementally maintained fold; identity when the group is empty."""
        return self._tree.product()

    def full_scan_product(self) -> Matrix:
        """Reference fold: plain left-to-right scan in stable sort order."""
        result = IDENTITY
        for pk in sorted(self._entries, key=self._sort_key):
            result = multiply(result, self._entries[pk][1], self.mod)
        return result
