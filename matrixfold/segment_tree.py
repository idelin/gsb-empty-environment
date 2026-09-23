"""Balanced interval (segment) tree folding an ordered matrix sequence.

The tree stores one 2x2 matrix per leaf; every internal node holds the
product ``left_child * right_child`` in that exact order, because matrix
multiplication is non-commutative. The root therefore holds the fold of
the whole sequence in index order.

There is intentionally no inverse-based removal: matrices may be singular
(e.g. determinant 0 mod m), so deletion is handled by rebuilding the
affected path / tree, never by multiplying with an inverse.
"""

from __future__ import annotations

from typing import Sequence

from .matrix import IDENTITY, Matrix, multiply, normalize


class SegmentTree:
    """Iterative segment tree over a fixed-length sequence of matrices."""

    def __init__(self, items: Sequence[Matrix], mod: int):
        if mod < 1:
            raise ValueError("modulus must be a positive integer")
        self.mod = mod
        self.length = len(items)
        # Number of leaves, padded up to a power of two; padding leaves are
        # identity matrices, which never change a product.
        self.size = max(1, 1 << (max(1, self.length) - 1).bit_length())
        self._tree: list[Matrix] = [IDENTITY] * (2 * self.size)
        for i, m in enumerate(items):
            self._tree[self.size + i] = normalize(m, mod)
        for i in range(self.size - 1, 0, -1):
            self._pull(i)

    def _pull(self, i: int) -> None:
        # Order matters: left child is the left factor.
        self._tree[i] = multiply(self._tree[2 * i], self._tree[2 * i + 1], self.mod)

    def set(self, index: int, value: Matrix) -> None:
        """Replace the matrix at ``index`` and repair the path to the root."""
        if not 0 <= index < self.length:
            raise IndexError(f"index {index} out of range for length {self.length}")
        i = self.size + index
        self._tree[i] = normalize(value, self.mod)
        i //= 2
        while i >= 1:
            self._pull(i)
            i //= 2

    def product(self) -> Matrix:
        """Fold of the whole sequence in order; identity for an empty tree."""
        return self._tree[1]
