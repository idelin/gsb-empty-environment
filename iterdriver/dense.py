"""Minimal dense linear algebra with explicit sizes and indices.

Pure standard library: no NumPy/SciPy. Every routine takes the logical
sizes explicitly and validates them, so shape mistakes fail loudly.
"""
from __future__ import annotations

import math


class ShapeError(ValueError):
    """Raised when a matrix/vector does not match its declared size."""


def check_matrix(a, n_rows, n_cols):
    if len(a) != n_rows:
        raise ShapeError(f"expected {n_rows} rows, got {len(a)}")
    for i in range(n_rows):
        if len(a[i]) != n_cols:
            raise ShapeError(
                f"row {i}: expected {n_cols} columns, got {len(a[i])}")


def check_vector(x, n):
    if len(x) != n:
        raise ShapeError(f"expected vector of size {n}, got {len(x)}")


def matvec(a, x, n_rows, n_cols):
    """y = A x with A of shape (n_rows, n_cols)."""
    check_matrix(a, n_rows, n_cols)
    check_vector(x, n_cols)
    return [sum(a[i][j] * x[j] for j in range(n_cols))
            for i in range(n_rows)]


def vec_sub(x, y, n):
    check_vector(x, n)
    check_vector(y, n)
    return [x[i] - y[i] for i in range(n)]


def norm2(x, n):
    check_vector(x, n)
    return math.sqrt(sum(x[i] * x[i] for i in range(n)))


def residual_norm(a, x, b, n_rows, n_cols):
    """||b - A x||_2, used as the independent dense verification."""
    ax = matvec(a, x, n_rows, n_cols)
    r = vec_sub(b, ax, n_rows)
    return norm2(r, n_rows)
