"""Self-written dense reference implementations (no NumPy/SciPy).

Used to verify the sparse triangular solver at small scales.
"""

from __future__ import annotations


def dense_from_coo(n, rows, cols, values):
    """Assemble a dense matrix from COO triplets, merging duplicates."""
    a = [[0.0] * n for _ in range(n)]
    merged = {}
    for r, c, v in zip(rows, cols, values):
        merged[(r, c)] = merged.get((r, c), 0.0) + float(v)
    for (r, c), v in merged.items():
        a[r][c] = v
    return a


def dense_solve_lower(a, b, unit_diagonal=False):
    """Reference forward substitution for a dense lower-triangular matrix."""
    n = len(a)
    x = [0.0] * n
    for i in range(n):
        acc = float(b[i])
        for j in range(i):
            acc -= a[i][j] * x[j]
        d = 1.0 if unit_diagonal else a[i][i]
        if d == 0.0:
            raise ZeroDivisionError(f"zero pivot at row {i}")
        x[i] = acc / d
    return x


def dense_solve_upper(a, b, unit_diagonal=False):
    """Reference back substitution for a dense upper-triangular matrix."""
    n = len(a)
    x = [0.0] * n
    for i in range(n - 1, -1, -1):
        acc = float(b[i])
        for j in range(i + 1, n):
            acc -= a[i][j] * x[j]
        d = 1.0 if unit_diagonal else a[i][i]
        if d == 0.0:
            raise ZeroDivisionError(f"zero pivot at row {i}")
        x[i] = acc / d
    return x


def transpose(a):
    """Dense transpose."""
    n = len(a)
    return [[a[r][c] for r in range(n)] for c in range(n)]


def matvec(a, x):
    """Dense matrix-vector product (residual checks)."""
    return [sum(aij * xj for aij, xj in zip(row, x)) for row in a]
