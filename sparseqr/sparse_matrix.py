"""Sparse matrix in column-dictionary form, built from explicit indices."""

from __future__ import annotations


class SparseMatrix:
    """Sparse matrix defined by explicit dimensions and (row, col, value) triples.

    Duplicate triples are summed. Zero values are dropped after accumulation.
    Internally stored as a list of per-column dicts mapping row index -> value.
    """

    def __init__(self, nrows, ncols, entries=()):
        if nrows < 0 or ncols < 0:
            raise ValueError("dimensions must be non-negative")
        self.nrows = int(nrows)
        self.ncols = int(ncols)
        cols = [dict() for _ in range(self.ncols)]
        for entry in entries:
            i, j, value = entry
            if not 0 <= i < self.nrows or not 0 <= j < self.ncols:
                raise IndexError(
                    f"entry index ({i}, {j}) out of range for "
                    f"{self.nrows}x{self.ncols} matrix"
                )
            if value != 0:
                cols[j][i] = cols[j].get(i, 0.0) + float(value)
        self._cols = [
            {i: v for i, v in col.items() if v != 0.0} for col in cols
        ]

    @property
    def shape(self):
        return (self.nrows, self.ncols)

    @property
    def nnz(self):
        return sum(len(col) for col in self._cols)

    def column(self, j):
        """Return a copy of column j as a {row: value} dict."""
        if not 0 <= j < self.ncols:
            raise IndexError(f"column {j} out of range")
        return dict(self._cols[j])

    def columns(self):
        """Return copies of all columns as {row: value} dicts."""
        return [dict(col) for col in self._cols]

    def matvec(self, x):
        """Compute A @ x."""
        if len(x) != self.ncols:
            raise ValueError(
                f"vector of length {len(x)} does not match {self.ncols} columns"
            )
        out = [0.0] * self.nrows
        for j, col in enumerate(self._cols):
            xj = x[j]
            if xj == 0.0:
                continue
            for i, v in col.items():
                out[i] += v * xj
        return out

    def to_dense(self):
        """Return a list-of-rows dense copy (mainly for tests/demos)."""
        dense = [[0.0] * self.ncols for _ in range(self.nrows)]
        for j, col in enumerate(self._cols):
            for i, v in col.items():
                dense[i][j] = v
        return dense
