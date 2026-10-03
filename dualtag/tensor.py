"""Minimal pure-Python dense tensor helpers (lists of lists of floats).

Only what the implicit solvers and tests need: matmul, matvec, transpose,
identity, inf-norm, and Gaussian elimination with partial pivoting that
reports numerical singularity instead of silently dividing by ~0.
"""
from __future__ import annotations

from . import budget as _budget
from .errors import SingularMatrixError


def zeros(rows, cols):
    return [[0.0] * cols for _ in range(rows)]


def identity(n):
    out = zeros(n, n)
    for i in range(n):
        out[i][i] = 1.0
    return out


def transpose(A):
    return [list(row) for row in zip(*A)]


def matmul(A, B):
    _budget.tick()
    Bt = transpose(B)
    return [[sum(a * b for a, b in zip(row, col)) for col in Bt] for row in A]


def matvec(A, v):
    _budget.tick()
    return [sum(a * b for a, b in zip(row, v)) for row in A]


def norm_inf(v):
    return max((abs(x) for x in v), default=0.0)


def solve(A, b, tol=1e-12):
    """Solve A x = b by Gaussian elimination with partial pivoting.

    Raises SingularMatrixError if any pivot is below tol.
    """
    n = len(A)
    M = []
    for i, row in enumerate(A):
        scale = max(abs(v) for v in row)
        if scale == 0.0:
            raise SingularMatrixError(f"singular matrix: zero row {i}")
        M.append([v / scale for v in row] + [float(b[i]) / scale])
    for col in range(n):
        _budget.tick()
        pivot = max(range(col, n), key=lambda r: abs(M[r][col]))
        if abs(M[pivot][col]) <= tol:
            raise SingularMatrixError(
                f"singular matrix: pivot {abs(M[pivot][col]):.3e} at column {col}"
            )
        M[col], M[pivot] = M[pivot], M[col]
        for r in range(col + 1, n):
            factor = M[r][col] / M[col][col]
            if factor != 0.0:
                for c in range(col, n + 1):
                    M[r][c] -= factor * M[col][c]
    x = [0.0] * n
    for r in range(n - 1, -1, -1):
        s = M[r][n] - sum(M[r][c] * x[c] for c in range(r + 1, n))
        x[r] = s / M[r][r]
    return x
