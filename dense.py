"""Tiny dense linear algebra helpers (standard library only).

Used to verify assembled systems at small scales without NumPy/SciPy.
"""

from __future__ import annotations


def solve_dense(matrix, rhs):
    """Solve A x = b by Gaussian elimination with partial pivoting."""
    n = len(matrix)
    if n == 0 or any(len(row) != n for row in matrix) or len(rhs) != n:
        raise ValueError("expected a square matrix and a matching rhs")
    a = [list(row) + [float(rhs[i])] for i, row in enumerate(matrix)]
    for col in range(n):
        pivot = max(range(col, n), key=lambda r: abs(a[r][col]))
        if abs(a[pivot][col]) < 1e-15:
            raise ValueError(f"singular matrix at column {col}")
        a[col], a[pivot] = a[pivot], a[col]
        for row in range(col + 1, n):
            factor = a[row][col] / a[col][col]
            if factor == 0.0:
                continue
            for k in range(col, n + 1):
                a[row][k] -= factor * a[col][k]
    x = [0.0] * n
    for row in range(n - 1, -1, -1):
        residual = a[row][n] - sum(a[row][k] * x[k] for k in range(row + 1, n))
        x[row] = residual / a[row][row]
    return x


def matvec(matrix, x):
    return [sum(row[k] * x[k] for k in range(len(x))) for row in matrix]
