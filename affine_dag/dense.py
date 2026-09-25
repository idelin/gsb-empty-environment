"""Tiny dense linear-algebra helpers with explicit sizes and indices.

Used only for small-scale cross-checking of the affine DAG (no NumPy).
All shapes are explicit inputs and are validated.
"""

from __future__ import annotations


def dot(a, b, n: int) -> float:
    if len(a) != n or len(b) != n:
        raise ValueError(f"dot: expected length {n}, got {len(a)} and {len(b)}")
    return sum(a[i] * b[i] for i in range(n))


def matvec(matrix, vector, m: int, n: int):
    """y[i] = sum_j matrix[i][j] * vector[j], i in 0..m-1, j in 0..n-1."""
    if len(matrix) != m:
        raise ValueError(f"matvec: expected {m} rows, got {len(matrix)}")
    for i in range(m):
        if len(matrix[i]) != n:
            raise ValueError(f"matvec: row {i} expected length {n}, got {len(matrix[i])}")
    if len(vector) != n:
        raise ValueError(f"matvec: expected vector length {n}, got {len(vector)}")
    return [dot(matrix[i], vector, n) for i in range(m)]


def matmul(a, b, m: int, k: int, n: int):
    """c[i][j] = sum_t a[i][t] * b[t][j]; shapes (m,k) x (k,n)."""
    if len(a) != m or any(len(row) != k for row in a):
        raise ValueError("matmul: left operand shape mismatch")
    if len(b) != k or any(len(row) != n for row in b):
        raise ValueError("matmul: right operand shape mismatch")
    return [
        [sum(a[i][t] * b[t][j] for t in range(k)) for j in range(n)]
        for i in range(m)
    ]
