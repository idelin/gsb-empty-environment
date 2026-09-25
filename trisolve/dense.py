"""Tiny dense triangular solver, written without NumPy/SciPy.

It serves two purposes:

* an independent reference to cross-check the sparse solver on small random
  problems (including transposed solves and unit diagonals);
* a residual helper ``residual_norm`` used by tests and demos.

It accepts either a :class:`~trisolve.matrix.TriangularMatrix` or a plain
dense ``list[list[float]]``.  Coordinate merging is applied when converting a
sparse matrix so both implementations see identical data.
"""

from __future__ import annotations

from .errors import DimensionError, ZeroPivotError
from .matrix import LOWER, TriangularMatrix


def to_dense(matrix: TriangularMatrix) -> list[list[float]]:
    n = matrix.n
    dense = [[0.0] * n for _ in range(n)]
    for e in matrix.entries:
        dense[e.row][e.col] += e.value
    if matrix.unit_diagonal:
        for i in range(n):
            dense[i][i] = 1.0
    return dense


def _effective_lower(uplo: str, transpose: bool) -> bool:
    return (uplo == LOWER) != transpose


def dense_solve(matrix_or_dense, b, *, transpose: bool = False,
                unit_diagonal: bool = False, uplo: str = LOWER,
                pivot_tol: float = 0.0) -> list[float]:
    """Forward/back substitution on a dense matrix (pure Python)."""
    if isinstance(matrix_or_dense, TriangularMatrix):
        m = matrix_or_dense
        a = to_dense(m)
        transpose = transpose
        unit_diagonal = m.unit_diagonal
        uplo = m.uplo
        pivot_tol = pivot_tol
    else:
        a = [list(row) for row in matrix_or_dense]

    n = len(a)
    if any(len(row) != n for row in a):
        raise DimensionError("dense matrix must be square")
    if len(b) != n:
        raise DimensionError(f"rhs length {len(b)} != n={n}")

    x = list(b)
    lower = _effective_lower(uplo, transpose)
    # A lower-triangular effective system is forward substitution; upper is
    # back substitution.  Coefficients come from the stored matrix, which is
    # A^T at (i, j) when a transpose was requested.
    index_order = range(n) if lower else range(n - 1, -1, -1)
    for i in index_order:
        for j in (range(i) if lower else range(n - 1, i, -1)):
            coeff = a[j][i] if transpose else a[i][j]
            x[i] -= coeff * x[j]
        pivot = 1.0 if unit_diagonal else a[i][i]
        if abs(pivot) <= pivot_tol:
            raise ZeroPivotError([i], pivot_tol)
        x[i] /= pivot
    return x


def dense_matvec(matrix_or_dense, x, *, transpose: bool = False) -> list[float]:
    """Compute A x (or A^T x) with no sparsity assumptions."""
    if isinstance(matrix_or_dense, TriangularMatrix):
        a = to_dense(matrix_or_dense)
    else:
        a = matrix_or_dense
    n = len(a)
    out = [0.0] * n
    for i in range(n):
        total = 0.0
        for j in range(n):
            coeff = a[j][i] if transpose else a[i][j]
            total += coeff * x[j]
        out[i] = total
    return out


def residual_norm(matrix: TriangularMatrix, x, b, *, transpose: bool = False) -> float:
    """max |A x - b| (or |A^T x - b|)."""
    ax = dense_matvec(matrix, x, transpose=transpose)
    return max(abs(ax[i] - b[i]) for i in range(len(b)))
