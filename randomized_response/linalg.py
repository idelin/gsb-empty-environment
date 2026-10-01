"""Minimal pure-Python linear algebra helpers (standard library only).

Only small dense matrices are handled (size = output alphabet size k),
which is the regime relevant for randomized-response channel matrices.
"""
from __future__ import annotations

import math


class SingularMatrixError(ValueError):
    """Raised when a square system cannot be solved because it is singular."""


def identity(n: int) -> list[list[float]]:
    return [[1.0 if i == j else 0.0 for j in range(n)] for i in range(n)]


def transpose(a: list[list[float]]) -> list[list[float]]:
    return [list(col) for col in zip(*a)]


def mat_vec(a: list[list[float]], x: list[float]) -> list[float]:
    return [sum(aij * xj for aij, xj in zip(row, x)) for row in a]


def mat_mul(a: list[list[float]], b: list[list[float]]) -> list[list[float]]:
    bt = transpose(b)
    return [[sum(x * y for x, y in zip(row, col)) for col in bt] for row in a]


def dot(x: list[float], y: list[float]) -> float:
    return sum(xi * yi for xi, yi in zip(x, y))


def solve(a: list[list[float]], b: list[float]) -> list[float]:
    """Solve a x = b by Gaussian elimination with partial pivoting."""
    n = len(a)
    aug = [list(map(float, row)) + [float(bi)] for row, bi in zip(a, b)]
    for col in range(n):
        pivot = max(range(col, n), key=lambda r: abs(aug[r][col]))
        if abs(aug[pivot][col]) < 1e-300:
            raise SingularMatrixError("matrix is singular to working precision")
        aug[col], aug[pivot] = aug[pivot], aug[col]
        piv = aug[col][col]
        for row in range(col + 1, n):
            factor = aug[row][col] / piv
            for j in range(col, n + 1):
                aug[row][j] -= factor * aug[col][j]
    x = [0.0] * n
    for row in range(n - 1, -1, -1):
        x[row] = (aug[row][n] - sum(aug[row][j] * x[j] for j in range(row + 1, n))) / aug[row][row]
    return x


def jacobi_eigen(a: list[list[float]], tol: float = 1e-14, max_sweeps: int = 200):
    """Eigen-decomposition of a real symmetric matrix via cyclic Jacobi rotations.

    Returns (eigenvalues, eigenvectors) sorted by descending eigenvalue, where
    eigenvectors[i] is the (unit) eigenvector for eigenvalues[i].
    """
    n = len(a)
    a = [list(map(float, row)) for row in a]
    v = identity(n)
    for _ in range(max_sweeps):
        off = math.sqrt(sum(a[i][j] ** 2 for i in range(n) for j in range(n) if i != j))
        if off <= tol:
            break
        for p in range(n - 1):
            for q in range(p + 1, n):
                if abs(a[p][q]) <= 1e-300:
                    continue
                app, aqq, apq = a[p][p], a[q][q], a[p][q]
                theta = (aqq - app) / (2.0 * apq)
                sign = 1.0 if theta >= 0 else -1.0
                t = sign / (abs(theta) + math.sqrt(theta * theta + 1.0))
                c = 1.0 / math.sqrt(t * t + 1.0)
                s = t * c
                for j in range(n):
                    if j != p and j != q:
                        ajp, ajq = a[j][p], a[j][q]
                        a[j][p] = a[p][j] = c * ajp - s * ajq
                        a[j][q] = a[q][j] = s * ajp + c * ajq
                a[p][p] = c * c * app - 2.0 * s * c * apq + s * s * aqq
                a[q][q] = s * s * app + 2.0 * s * c * apq + c * c * aqq
                a[p][q] = a[q][p] = 0.0
                for j in range(n):
                    vjp, vjq = v[j][p], v[j][q]
                    v[j][p] = c * vjp - s * vjq
                    v[j][q] = s * vjp + c * vjq
    order = sorted(range(n), key=lambda i: -a[i][i])
    values = [a[i][i] for i in order]
    vectors = [[v[row][i] for row in range(n)] for i in order]
    return values, vectors


def project_to_simplex(y: list[float]) -> list[float]:
    """Euclidean projection onto the probability simplex {x >= 0, sum x = 1}.

    Uses the O(k log k) sort-based algorithm of Duchi et al. (2008).
    """
    n = len(y)
    if n == 1:
        return [1.0]
    u = sorted(y, reverse=True)
    css = 0.0
    theta = 0.0
    for i, ui in enumerate(u):
        css += ui
        t = (css - 1.0) / (i + 1)
        if ui - t > 0.0:
            theta = t
    return [max(yi - theta, 0.0) for yi in y]
