"""Self-written dense linear algebra.

Matrices are ``list[list[float]]`` in row-major form.  Sizes are always
explicit; no NumPy/SciPy is used anywhere in this project.
"""
from __future__ import annotations


def zeros(nrows: int, ncols: int) -> list[list[float]]:
    return [[0.0] * ncols for _ in range(nrows)]


def identity(n: int) -> list[list[float]]:
    out = zeros(n, n)
    for i in range(n):
        out[i][i] = 1.0
    return out


def shape(A: list[list[float]]) -> tuple[int, int]:
    return (len(A), len(A[0]) if A else 0)


def from_entries(nrows: int, ncols: int, entries: dict) -> list[list[float]]:
    """Build a dense matrix from a ``{(row, col): value}`` mapping."""
    A = zeros(nrows, ncols)
    for (r, c), v in entries.items():
        if not (0 <= r < nrows and 0 <= c < ncols):
            raise IndexError(f"entry {(r, c)} outside {nrows}x{ncols}")
        A[r][c] = v
    return A


def matvec(A: list[list[float]], x: list[float]) -> list[float]:
    if A and len(A[0]) != len(x):
        raise ValueError("matvec dimension mismatch")
    return [sum(aij * xj for aij, xj in zip(row, x)) for row in A]


def matmul(A: list[list[float]], B: list[list[float]]) -> list[list[float]]:
    if A and B and len(A[0]) != len(B):
        raise ValueError("matmul dimension mismatch")
    Bt = list(zip(*B)) if B else []
    return [[sum(a * b for a, b in zip(row, col)) for col in Bt] for row in A]


def abs_matrix(A: list[list[float]]) -> list[list[float]]:
    return [[abs(v) for v in row] for row in A]


def linf_norm(x: list[float]) -> float:
    return max((abs(v) for v in x), default=0.0)
