"""Pure-Python float linear algebra helpers (standard library only).

All matrices are ``list[list[float]]`` in row-major order.  These helpers are
deliberately small and dependency-free so the package runs on a bare
Python 3.11+ interpreter.
"""
from __future__ import annotations

import math

Matrix = list[list[float]]
Vector = list[float]


def as_matrix(rows, *, name: str = "matrix") -> Matrix:
    """Validate and copy a rectangular matrix of real numbers."""
    matrix = [[float(x) for x in row] for row in rows]
    if not matrix:
        raise ValueError(f"{name} must have at least one row")
    width = len(matrix[0])
    if width == 0:
        raise ValueError(f"{name} must have at least one column")
    for row in matrix:
        if len(row) != width:
            raise ValueError(f"{name} is not rectangular")
    return matrix


def eye(n: int) -> Matrix:
    return [[1.0 if i == j else 0.0 for j in range(n)] for i in range(n)]


def transpose(a: Matrix) -> Matrix:
    return [list(col) for col in zip(*a)]


def matmul(a: Matrix, b: Matrix) -> Matrix:
    bt = transpose(b)
    return [[sum(x * y for x, y in zip(row, col)) for col in bt] for row in a]


def matvec(a: Matrix, v: Vector) -> Vector:
    return [sum(x * y for x, y in zip(row, v)) for row in a]


def dot(u: Vector, v: Vector) -> float:
    return sum(x * y for x, y in zip(u, v))


def norm2(v: Vector) -> float:
    return math.sqrt(dot(v, v))


def sub(a: Matrix, b: Matrix) -> Matrix:
    return [[x - y for x, y in zip(ra, rb)] for ra, rb in zip(a, b)]


def frobenius(a: Matrix) -> float:
    return math.sqrt(sum(x * x for row in a for x in row))


def max_abs(a: Matrix) -> float:
    return max((abs(x) for row in a for x in row), default=0.0)


def symmetrize(a: Matrix) -> Matrix:
    n = len(a)
    return [[0.5 * (a[i][j] + a[j][i]) for j in range(n)] for i in range(n)]


def asymmetry(a: Matrix) -> float:
    """Largest |a[i][j] - a[j][i]|; 0 for exactly symmetric input."""
    n = len(a)
    return max(
        (abs(a[i][j] - a[j][i]) for i in range(n) for j in range(i + 1, n)),
        default=0.0,
    )


def quadratic_form(v: Vector, a: Matrix) -> float:
    return dot(v, matvec(a, v))


def jacobi_eigen(a: Matrix, *, tol: float = 1e-13, max_sweeps: int = 100):
    """Eigendecomposition of a real symmetric matrix via cyclic Jacobi sweeps.

    Returns ``(values, vectors)`` with eigenvalues sorted descending and
    ``vectors[i]`` the unit eigenvector for ``values[i]``.
    """
    n = len(a)
    m = [row[:] for row in a]
    v = eye(n)
    for _ in range(max_sweeps):
        off = math.sqrt(sum(m[i][j] ** 2 for i in range(n) for j in range(n) if i != j))
        diag = math.sqrt(sum(m[i][i] ** 2 for i in range(n)))
        if off <= tol * max(1.0, diag):
            break
        for p in range(n - 1):
            for q in range(p + 1, n):
                apq = m[p][q]
                if apq == 0.0:
                    continue
                theta = (m[q][q] - m[p][p]) / (2.0 * apq)
                sign = 1.0 if theta >= 0.0 else -1.0
                t = sign / (abs(theta) + math.sqrt(theta * theta + 1.0))
                c = 1.0 / math.sqrt(1.0 + t * t)
                s = t * c
                for k in range(n):
                    mkp, mkq = m[k][p], m[k][q]
                    m[k][p] = c * mkp - s * mkq
                    m[k][q] = s * mkp + c * mkq
                for k in range(n):
                    mpk, mqk = m[p][k], m[q][k]
                    m[p][k] = c * mpk - s * mqk
                    m[q][k] = s * mpk + c * mqk
                for k in range(n):
                    vkp, vkq = v[k][p], v[k][q]
                    v[k][p] = c * vkp - s * vkq
                    v[k][q] = s * vkp + c * vkq
    order = sorted(range(n), key=lambda i: -m[i][i])
    values = [m[i][i] for i in order]
    vectors = [[v[row][i] for row in range(n)] for i in order]
    return values, vectors
