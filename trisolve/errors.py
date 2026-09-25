"""Exceptions for the sparse triangular solver.

Two failure classes are deliberately distinct:

* :class:`StructureError` and its subclass :class:`MissingPivotError` are
  symbolic problems raised while the matrix is built or analyzed.  No numeric
  work has started when they are raised.
* :class:`ZeroPivotError` is a purely numerical failure raised during a solve.
  The working result is discarded before the exception propagates, so the
  caller never observes a partially computed right-hand side.
"""

from __future__ import annotations


class TriSolverError(Exception):
    """Base class for every error raised by this package."""


class StructureError(TriSolverError):
    """The matrix structure violates the solver's requirements.

    Examples are out-of-range indices, entries outside the declared triangle,
    a missing diagonal entry, or an explicit diagonal entry on a matrix
    declared as unit diagonal.
    """


class MissingPivotError(StructureError):
    """A diagonal (pivot position) is absent from the matrix structure."""

    def __init__(self, missing: list[int]) -> None:
        self.missing = sorted(missing)
        shown = ", ".join(str(i) for i in self.missing[:10])
        tail = "" if len(self.missing) <= 10 else f", ... (+{len(self.missing) - 10} more)"
        super().__init__(f"missing diagonal pivots at rows: {shown}{tail}")


class ZeroPivotError(TriSolverError):
    """A diagonal pivot is numerically zero (or too close to zero).

    Unlike a missing pivot, the structure is valid; only the stored values
    make the solve impossible.  No partial solution is published.
    """

    def __init__(self, pivots: list[int], pivot_tol: float) -> None:
        self.pivots = sorted(pivots)
        self.pivot_tol = pivot_tol
        shown = ", ".join(str(i) for i in self.pivots[:10])
        tail = "" if len(self.pivots) <= 10 else f", ... (+{len(self.pivots) - 10} more)"
        super().__init__(
            f"zero or near-zero pivots (<= {pivot_tol:g}) at rows: {shown}{tail}"
        )


class DimensionError(StructureError):
    """A vector/matrix dimension does not match the solved system."""
