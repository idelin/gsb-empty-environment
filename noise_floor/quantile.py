"""Exact sliding-window quantile backbone.

A sorted list maintained with a hand-rolled binary search. Every element
comparison is counted so tests can prove the incremental structure performs
O(log n) comparisons per update instead of an O(n log n) full re-sort per
frame. An independent full-sort oracle is provided for small-scale
cross-checking only.
"""

from __future__ import annotations

import math


class SortedMultiset:
    """Sorted multiset of floats with a comparison counter."""

    __slots__ = ("_items", "comparisons")

    def __init__(self, items=()):
        self._items = sorted(items)
        self.comparisons = 0

    def __len__(self) -> int:
        return len(self._items)

    def _bisect_left(self, value: float) -> int:
        lo, hi = 0, len(self._items)
        items = self._items
        while lo < hi:
            mid = (lo + hi) // 2
            self.comparisons += 1
            if items[mid] < value:
                lo = mid + 1
            else:
                hi = mid
        return lo

    def add(self, value: float) -> None:
        self._items.insert(self._bisect_left(value), value)

    def discard(self, value: float) -> bool:
        idx = self._bisect_left(value)
        if idx < len(self._items) and self._items[idx] == value:
            del self._items[idx]
            return True
        return False

    def quantile(self, q: float):
        """Exact quantile with linear interpolation; None when empty."""
        n = len(self._items)
        if n == 0:
            return None
        if n == 1:
            return self._items[0]
        pos = q * (n - 1)
        lo = int(pos)
        hi = min(lo + 1, n - 1)
        frac = pos - lo
        return self._items[lo] * (1.0 - frac) + self._items[hi] * frac

    def sorted_items(self) -> list:
        return list(self._items)

    @classmethod
    def from_sorted(cls, items) -> "SortedMultiset":
        ms = cls()
        ms._items = list(items)
        return ms


def oracle_quantile(values, q: float):
    """Independent full-sort oracle. Small-scale verification use only."""
    vals = sorted(values)
    if not vals:
        return None
    if len(vals) == 1:
        return vals[0]
    pos = q * (len(vals) - 1)
    lo = int(pos)
    hi = min(lo + 1, len(vals) - 1)
    frac = pos - lo
    return vals[lo] * (1.0 - frac) + vals[hi] * frac


def max_comparisons_per_update(n: int) -> int:
    """Declared comparison budget for one incremental update at size n."""
    return math.ceil(math.log2(max(n, 1))) + 2
