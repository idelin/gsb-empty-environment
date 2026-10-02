"""Explicit-index CSR sparse matrix. Pure standard library.

Sizes and indices are always given explicitly; nothing is inferred.
The matrix carries a monotonically increasing ``version`` counter that
is bumped by every in-place mutation, so the solver can detect that the
operator changed and refuse to reuse stale Krylov state.
"""

from __future__ import annotations

import math


class SparseMatrix:
    """CSR matrix: n_rows, n_cols, indptr, indices, values are all explicit."""

    def __init__(self, n_rows, n_cols, indptr, indices, values):
        if n_rows <= 0 or n_cols <= 0:
            raise ValueError(f"dimensions must be positive, got {n_rows}x{n_cols}")
        if len(indptr) != n_rows + 1:
            raise ValueError(
                f"indptr must have n_rows+1={n_rows + 1} entries, got {len(indptr)}"
            )
        if indptr[0] != 0 or indptr[-1] != len(indices):
            raise ValueError("indptr must start at 0 and end at len(indices)")
        if len(indices) != len(values):
            raise ValueError(
                f"indices and values must have equal length, "
                f"got {len(indices)} and {len(values)}"
            )
        prev = 0
        for row, end in enumerate(indptr[1:]):
            if end < prev:
                raise ValueError(f"indptr must be non-decreasing (row {row})")
            prev = end
        for k, j in enumerate(indices):
            if not 0 <= j < n_cols:
                raise ValueError(f"column index out of range at position {k}: {j}")
        self.n_rows = n_rows
        self.n_cols = n_cols
        self.indptr = list(indptr)
        self.indices = list(indices)
        self.values = [float(v) for v in values]
        self._version = 0

    @property
    def version(self):
        """Monotonic token; every mutation bumps it."""
        return self._version

    def update_value(self, row, col, value):
        """In-place update of an existing structural entry; bumps version."""
        if not 0 <= row < self.n_rows:
            raise IndexError(f"row {row} out of range")
        for k in range(self.indptr[row], self.indptr[row + 1]):
            if self.indices[k] == col:
                self.values[k] = float(value)
                self._version += 1
                return
        raise KeyError(f"no structural entry at ({row}, {col})")

    def matvec(self, x):
        if len(x) != self.n_cols:
            raise ValueError(
                f"matvec dimension mismatch: A is {self.n_rows}x{self.n_cols}, "
                f"x has length {len(x)}"
            )
        y = [0.0] * self.n_rows
        for i in range(self.n_rows):
            acc = 0.0
            for k in range(self.indptr[i], self.indptr[i + 1]):
                acc += self.values[k] * x[self.indices[k]]
            y[i] = acc
        return y

    def diagonal(self):
        """Explicit diagonal; missing structural entries come back as 0.0."""
        d = [0.0] * min(self.n_rows, self.n_cols)
        for i in range(min(self.n_rows, self.n_cols)):
            for k in range(self.indptr[i], self.indptr[i + 1]):
                if self.indices[k] == i:
                    d[i] = self.values[k]
                    break
        return d

    def symmetry_defect(self):
        """Max |a_ij - a_ji| over all pairs, with concrete evidence.

        Returns (defect, i, j, a_ij, a_ji, scale) where scale = max |a_ij|.
        A structurally or numerically asymmetric matrix yields defect > 0.
        """
        entries = {}
        scale = 0.0
        for i in range(self.n_rows):
            for k in range(self.indptr[i], self.indptr[i + 1]):
                j = self.indices[k]
                v = self.values[k]
                if j < self.n_rows:  # only intra-square pairs matter
                    entries[(i, j)] = entries.get((i, j), 0.0) + v
                scale = max(scale, abs(v))
        defect, bi, bj, bij, bji = 0.0, -1, -1, 0.0, 0.0
        for (i, j), vij in entries.items():
            vji = entries.get((j, i), 0.0)
            d = abs(vij - vji)
            if d > defect:
                defect, bi, bj, bij, bji = d, i, j, vij, vji
        return defect, bi, bj, bij, bji, scale

    def to_dense(self):
        dense = [[0.0] * self.n_cols for _ in range(self.n_rows)]
        for i in range(self.n_rows):
            for k in range(self.indptr[i], self.indptr[i + 1]):
                dense[i][self.indices[k]] += self.values[k]
        return dense
