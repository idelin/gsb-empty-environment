"""Updatable diagonal (Jacobi) preconditioner.

The preconditioner is a plain vector of diagonal entries that can be
replaced in place (``update``) when the matrix changes, without
rebuilding any other solver structure.
"""

from __future__ import annotations


class DiagonalPreconditioner:
    def __init__(self, diagonal):
        if len(diagonal) == 0:
            raise ValueError("preconditioner diagonal must be non-empty")
        self._diag = [float(d) for d in diagonal]

    @classmethod
    def from_matrix(cls, matrix):
        return cls(matrix.diagonal())

    @property
    def size(self):
        return len(self._diag)

    def update(self, diagonal):
        """Hot-update the diagonal after a matrix version change."""
        if len(diagonal) != len(self._diag):
            raise ValueError(
                f"updated diagonal must keep size {len(self._diag)}, "
                f"got {len(diagonal)}"
            )
        self._diag = [float(d) for d in diagonal]

    def update_from_matrix(self, matrix):
        self.update(matrix.diagonal())

    def zero_entry_index(self):
        """Index of the first exactly-zero diagonal entry, or None."""
        for i, d in enumerate(self._diag):
            if d == 0.0:
                return i
        return None

    def apply(self, r):
        if len(r) != len(self._diag):
            raise ValueError(
                f"preconditioner size {len(self._diag)} != vector size {len(r)}"
            )
        return [ri / di for ri, di in zip(r, self._diag)]
