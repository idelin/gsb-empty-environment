"""Small dense reference routines used to cross-check the sparse solver.

Gaussian elimination with partial pivoting; only meant for small n.
"""

from __future__ import annotations


def dense_solve(a, b):
    """Solve a x = b by Gaussian elimination with partial pivoting."""
    n = len(a)
    if n == 0 or any(len(row) != n for row in a) or len(b) != n:
        raise ValueError("dense_solve expects a square matrix and matching rhs")
    m = [list(row) + [float(bi)] for row, bi in zip(a, b)]
    for col in range(n):
        piv = max(range(col, n), key=lambda r: abs(m[r][col]))
        if m[piv][col] == 0.0:
            raise ZeroDivisionError(f"singular matrix: zero pivot at column {col}")
        m[col], m[piv] = m[piv], m[col]
        pivot = m[col][col]
        for r in range(col + 1, n):
            factor = m[r][col] / pivot
            if factor == 0.0:
                continue
            for c in range(col, n + 1):
                m[r][c] -= factor * m[col][c]
    x = [0.0] * n
    for i in range(n - 1, -1, -1):
        acc = m[i][n]
        for c in range(i + 1, n):
            acc -= m[i][c] * x[c]
        x[i] = acc / m[i][i]
    return x
