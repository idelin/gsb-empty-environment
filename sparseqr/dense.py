"""Self-contained dense reference QR (modified Gram-Schmidt with column
pivoting). Used to cross-check the sparse implementation on small problems.
"""

from __future__ import annotations

import math
import sys


def _dot(u, v):
    return sum(a * b for a, b in zip(u, v))


def dense_qr_least_squares(a, b, tol=None):
    """Solve min ||a x - b||_2 for a dense row-major matrix a.

    Returns (x, rank, residual_norm, perm) with x in original order.
    """
    m = len(a)
    n = len(a[0]) if a else 0
    if len(b) != m:
        raise ValueError("dimension mismatch")
    cols = [[float(a[i][j]) for i in range(m)] for j in range(n)]
    perm = list(range(n))
    r = [[0.0] * n for _ in range(n)]
    qcols = []
    steps = min(m, n)
    for k in range(steps):
        p = max(range(k, n), key=lambda j: _dot(cols[j], cols[j]))
        if p != k:
            cols[k], cols[p] = cols[p], cols[k]
            perm[k], perm[p] = perm[p], perm[k]
            for i in range(k):
                r[i][k], r[i][p] = r[i][p], r[i][k]
        norm = math.sqrt(_dot(cols[k], cols[k]))
        r[k][k] = norm
        if norm == 0.0:
            qcols.append([0.0] * m)
            continue
        q = [value / norm for value in cols[k]]
        qcols.append(q)
        for j in range(k + 1, n):
            r[k][j] = _dot(q, cols[j])
            cols[j] = [cj - r[k][j] * qi for cj, qi in zip(cols[j], q)]

    max_diag = max((abs(r[k][k]) for k in range(steps)), default=0.0)
    if tol is None:
        tol = max(m, n) * sys.float_info.epsilon * max_diag
    rank = 0
    for k in range(steps):
        if abs(r[k][k]) > tol:
            rank += 1
        else:
            break

    y = [_dot(qcols[k], b) for k in range(steps)]
    x_perm = [0.0] * n
    for i in range(rank - 1, -1, -1):
        s = y[i] - sum(r[i][j] * x_perm[j] for j in range(i + 1, rank))
        x_perm[i] = s / r[i][i]

    x = [0.0] * n
    for k in range(n):
        x[perm[k]] = x_perm[k]
    residual = [
        sum(a[i][j] * x[j] for j in range(n)) - b[i] for i in range(m)
    ]
    return x, rank, math.sqrt(_dot(residual, residual)), perm
