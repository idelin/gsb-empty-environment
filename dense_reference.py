"""Self-contained dense QR least-squares reference implementation.

Used to cross-check the sparse solver on small problems.  Same approach
(column-pivoted Householder QR + back substitution, no inversion) but on
plain dense row lists, written independently of sparse_qr.py.
"""

from __future__ import annotations

import math

_MACHINE_EPS = 2.220446049250313e-16


def dense_least_squares(A_rows, b, tol=None):
    """Solve min_x ||A x - b||_2 for dense A given as a list of rows.

    Returns (x, rank, residual_norm, rank_deficient).
    """
    m = len(A_rows)
    n = len(A_rows[0])
    a = [[float(v) for v in row] for row in A_rows]
    b = [float(v) for v in b]
    perm = list(range(n))

    scale = max((math.sqrt(sum(a[i][j] ** 2 for i in range(m)))
                 for j in range(n)), default=0.0)
    if tol is None:
        tol = max(m, n) * _MACHINE_EPS * scale

    householders = []
    r_diag = []
    rank = 0

    for k in range(min(m, n)):
        p = max(range(k, n),
                key=lambda j: sum(a[i][j] ** 2 for i in range(k, m)))
        if math.sqrt(sum(a[i][p] ** 2 for i in range(k, m))) <= tol:
            break
        if p != k:
            for i in range(m):
                a[i][k], a[i][p] = a[i][p], a[i][k]
            perm[k], perm[p] = perm[p], perm[k]

        xk = a[k][k]
        norm = math.sqrt(sum(a[i][k] ** 2 for i in range(k, m)))
        alpha = -math.copysign(norm, xk) if xk != 0.0 else -norm
        v = [0.0] * m
        for i in range(k, m):
            v[i] = a[i][k]
        v[k] -= alpha
        beta = 2.0 / sum(vi * vi for vi in v)
        householders.append((v, beta))
        r_diag.append(alpha)

        for j in range(k + 1, n):
            s = beta * sum(v[i] * a[i][j] for i in range(k, m))
            for i in range(k, m):
                a[i][j] -= s * v[i]
        rank = k + 1

    qb = list(b)
    for v, beta in householders:
        k = next(i for i, vi in enumerate(v) if vi != 0.0)
        s = beta * sum(v[i] * qb[i] for i in range(k, m))
        for i in range(k, m):
            qb[i] -= s * v[i]

    z = [0.0] * rank
    for k in range(rank - 1, -1, -1):
        acc = qb[k] - sum(a[k][j] * z[j] for j in range(k + 1, rank))
        z[k] = acc / r_diag[k]

    x = [0.0] * n
    for k in range(rank):
        x[perm[k]] = z[k]

    residual = [b[i] - sum(A_rows[i][j] * x[j] for j in range(n))
                for i in range(m)]
    return x, rank, math.sqrt(sum(r * r for r in residual)), rank < n
