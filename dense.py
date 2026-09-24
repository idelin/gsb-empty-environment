"""Pure-Python dense linear algebra helpers (standard library only).

Provides Cholesky factorization with rank-1 update / downdate so that
the normal-equation factor can be maintained incrementally instead of
being rebuilt from scratch on every observation change.
"""

import math

__all__ = [
    "NotPositiveDefiniteError",
    "DowndateError",
    "cholesky",
    "chol_rank1_update",
    "chol_rank1_downdate",
    "forward_solve",
    "chol_solve",
    "matvec",
    "rref_solve",
]


class NotPositiveDefiniteError(ArithmeticError):
    """Raised when a matrix is numerically not positive definite."""


class DowndateError(ArithmeticError):
    """Raised when a rank-1 downdate would destroy positive definiteness."""


def cholesky(a):
    """Lower Cholesky factor L with L @ L.T == a. Raises if not PD."""
    n = len(a)
    L = [[0.0] * n for _ in range(n)]
    for i in range(n):
        for j in range(i + 1):
            s = 0.0
            row_i, row_j = a[i], a[j]
            Li, Lj = L[i], L[j]
            for k in range(j):
                s += Li[k] * Lj[k]
            if i == j:
                d = row_i[i] - s
                if d <= 0.0:
                    raise NotPositiveDefiniteError(
                        "pivot %d non-positive (%r)" % (i, d))
                Li[j] = math.sqrt(d)
            else:
                Li[j] = (row_i[j] - s) / Lj[j]
    return L


def chol_rank1_update(L, x):
    """Return new lower factor of L @ L.T + x @ x.T (x is modified copy)."""
    n = len(L)
    new = [row[:] for row in L]
    x = list(x)
    for k in range(n):
        Lkk = new[k][k]
        r = math.hypot(Lkk, x[k])
        c = r / Lkk
        s = x[k] / Lkk
        new[k][k] = r
        for j in range(k + 1, n):
            Ljk = (new[j][k] + s * x[j]) / c
            new[j][k] = Ljk
            x[j] = c * x[j] - s * Ljk
    return new


def chol_rank1_downdate(L, a):
    """Return new lower factor of L @ L.T - a @ a.T.

    Raises DowndateError if the result would not be positive definite
    (i.e. the downdate drops numerical rank).
    """
    x = forward_solve(L, a)
    n = len(L)
    new = [row[:] for row in L]
    for k in range(n):
        Lkk = new[k][k]
        d = Lkk * Lkk - x[k] * x[k]
        if d <= 0.0:
            raise DowndateError(
                "downdate loses positive definiteness at pivot %d" % k)
        r = math.sqrt(d)
        c = r / Lkk
        s = x[k] / Lkk
        new[k][k] = r
        for j in range(k + 1, n):
            Ljk = (new[j][k] - s * x[j]) / c
            new[j][k] = Ljk
            x[j] = c * x[j] - s * Ljk
    return new


def forward_solve(L, b):
    """Solve L x = b for lower-triangular L."""
    n = len(L)
    x = [0.0] * n
    for i in range(n):
        s = b[i]
        Li = L[i]
        for k in range(i):
            s -= Li[k] * x[k]
        x[i] = s / Li[i]
    return x


def backward_solve(L, b):
    """Solve L.T x = b for lower-triangular L."""
    n = len(L)
    x = [0.0] * n
    for i in range(n - 1, -1, -1):
        s = b[i]
        for k in range(i + 1, n):
            s -= L[k][i] * x[k]
        x[i] = s / L[i][i]
    return x


def chol_solve(L, b):
    """Solve (L @ L.T) x = b given the lower Cholesky factor L."""
    return backward_solve(L, forward_solve(L, b))


def matvec(a, x):
    return [sum(row[j] * x[j] for j in range(len(x))) for row in a]


def rref_solve(M, b, tol=None):
    """Solve a (possibly singular) consistent symmetric system M x = b.

    Returns (particular, null_basis, rank) where the full solution set is
    particular + span(null_basis). Uses Gauss-Jordan with partial pivoting.
    """
    n = len(M)
    if tol is None:
        scale = max((abs(M[i][i]) for i in range(n)), default=0.0)
        tol = 1e-12 * max(1.0, scale)
    aug = [list(M[i]) + [b[i]] for i in range(n)]
    pivots = []
    row = 0
    for col in range(n):
        best, best_val = -1, tol
        for r in range(row, n):
            v = abs(aug[r][col])
            if v > best_val:
                best, best_val = r, v
        if best < 0:
            continue
        aug[row], aug[best] = aug[best], aug[row]
        inv = 1.0 / aug[row][col]
        aug[row] = [v * inv for v in aug[row]]
        for r in range(n):
            if r != row and aug[r][col] != 0.0:
                f = aug[r][col]
                aug[r] = [rv - f * pv for rv, pv in zip(aug[r], aug[row])]
        pivots.append(col)
        row += 1
        if row == n:
            break
    rank = len(pivots)
    for r in range(rank, n):
        if abs(aug[r][n]) > 1e-8 * max(1.0, max(abs(v) for v in b)):
            raise ArithmeticError("system is inconsistent")
    particular = [0.0] * n
    for i, p in enumerate(pivots):
        particular[p] = aug[i][n]
    pivot_set = set(pivots)
    null_basis = []
    for f in range(n):
        if f in pivot_set:
            continue
        vec = [0.0] * n
        vec[f] = 1.0
        for i, p in enumerate(pivots):
            vec[p] = -aug[i][f]
        null_basis.append(vec)
    return particular, null_basis, rank
