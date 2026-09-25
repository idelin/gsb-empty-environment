"""Rank-1 / low-rank update solves on top of a pre-factored dense system.

Given an LU factorization of A, solve (A + u v^T) x = b (rank-1,
Sherman-Morrison) or (A + U V^T) x = b (rank-k, Woodbury) without
re-factoring A.  Updates that make the system (nearly) singular are
detected through the Sherman-Morrison denominator / Woodbury capacitance
matrix; on failure the original factorization is untouched and stays
usable.  Pure standard library, no NumPy/SciPy.
"""

from __future__ import annotations

import warnings

__all__ = [
    "SingularMatrixError",
    "SingularUpdateError",
    "UnstableUpdateWarning",
    "LUFactorization",
    "lu_factor",
    "lu_solve",
    "lu_solve_many",
    "sparse_vector",
    "matvec",
    "sherman_morrison_solve",
    "sherman_morrison_solve_many",
    "sherman_morrison_reverse_solve",
    "woodbury_solve",
    "dense_reference_solve",
]

# Hard threshold: |denominator| below this is treated as a singular update.
SING_TOL = 1e-12
# Soft threshold: below this the update is solvable but numerically unstable.
STAB_TOL = 1e-8


class SingularMatrixError(Exception):
    """Raised when a matrix cannot be factored (zero pivot)."""


class SingularUpdateError(Exception):
    """Raised when a low-rank update makes the system singular."""


class UnstableUpdateWarning(UserWarning):
    """Issued when the update denominator is dangerously small."""


def lu_factor(a):
    """LU factorization with partial pivoting of a square dense matrix.

    ``a`` is a list of ``n`` row lists of length ``n``.  Returns
    ``(lu, piv)`` where ``lu`` stores L (unit diagonal, below) and U
    (on/above diagonal) combined, and ``piv`` is the row permutation such
    that ``P A = L U``.  Raises :class:`SingularMatrixError` on a zero
    pivot.
    """
    n = len(a)
    if n == 0 or any(len(row) != n for row in a):
        raise ValueError("matrix must be non-empty and square")
    lu = [list(row) for row in a]
    piv = list(range(n))
    for k in range(n):
        p = max(range(k, n), key=lambda i: abs(lu[i][k]))
        if abs(lu[p][k]) == 0.0:
            raise SingularMatrixError(f"zero pivot at column {k}")
        if p != k:
            lu[k], lu[p] = lu[p], lu[k]
            piv[k], piv[p] = piv[p], piv[k]
        for i in range(k + 1, n):
            lu[i][k] /= lu[k][k]
            factor = lu[i][k]
            if factor != 0.0:
                row_i = lu[i]
                row_k = lu[k]
                for j in range(k + 1, n):
                    row_i[j] -= factor * row_k[j]
    return lu, piv


def lu_solve(lu, piv, b):
    """Solve A x = b given ``(lu, piv)`` from :func:`lu_factor`."""
    n = len(lu)
    if len(b) != n:
        raise ValueError("right-hand side has wrong length")
    x = [float(b[piv[i]]) for i in range(n)]
    for i in range(1, n):  # forward substitution, unit L
        s = x[i]
        row = lu[i]
        for j in range(i):
            s -= row[j] * x[j]
        x[i] = s
    for i in range(n - 1, -1, -1):  # back substitution, U
        s = x[i]
        row = lu[i]
        for j in range(i + 1, n):
            s -= row[j] * x[j]
        x[i] = s / row[i]
    return x


def lu_solve_many(lu, piv, bcols):
    """Solve A X = B for multiple right-hand sides.

    ``bcols`` is a list of column vectors; returns a list of solution
    columns in the same order.
    """
    return [lu_solve(lu, piv, b) for b in bcols]


class LUFactorization:
    """Reusable LU factorization of a dense square matrix."""

    def __init__(self, a):
        self.n = len(a)
        self.lu, self.piv = lu_factor(a)

    def solve(self, b):
        return lu_solve(self.lu, self.piv, b)

    def solve_many(self, bcols):
        return lu_solve_many(self.lu, self.piv, bcols)


def sparse_vector(n, indices, values):
    """Build a dense vector of size ``n`` from explicit indices/values."""
    if len(indices) != len(values):
        raise ValueError("indices and values must have equal length")
    vec = [0.0] * n
    for i, v in zip(indices, values):
        if not 0 <= i < n:
            raise IndexError(f"index {i} out of range for size {n}")
        vec[i] += float(v)
    return vec


def matvec(a, x):
    """Dense matrix-vector product, used by the self-check helpers."""
    return [sum(row[j] * x[j] for j in range(len(x))) for row in a]


def _dot(a, b):
    return sum(ai * bi for ai, bi in zip(a, b))


def _check_denominator(denom, sing_tol, stab_tol, label):
    if abs(denom) <= sing_tol:
        raise SingularUpdateError(
            f"{label}: update makes the system singular "
            f"(denominator {denom:.3e}); original factorization is unchanged"
        )
    if abs(denom) <= stab_tol:
        warnings.warn(
            f"{label}: denominator {denom:.3e} is close to 0; "
            "update is numerically unstable",
            UnstableUpdateWarning,
            stacklevel=3,
        )


def _sherman_morrison_coeffs(fact, u, v, sing_tol, stab_tol, label):
    n = fact.n
    if len(u) != n or len(v) != n:
        raise ValueError("update vectors must match the factored size")
    w = fact.solve(u)  # w = A^{-1} u
    denom = 1.0 + _dot(v, w)
    _check_denominator(denom, sing_tol, stab_tol, label)
    return w, denom


def sherman_morrison_solve(fact, u, v, b, *, sing_tol=SING_TOL, stab_tol=STAB_TOL):
    """Solve (A + u v^T) x = b using the factorization of A.

    ``fact`` is an :class:`LUFactorization` of A; ``u`` and ``v`` are
    dense vectors of the factored size.  The factorization is never
    modified, so a :class:`SingularUpdateError` leaves it fully usable.
    """
    w, denom = _sherman_morrison_coeffs(fact, u, v, sing_tol, stab_tol,
                                        "sherman_morrison_solve")
    y = fact.solve(b)  # y = A^{-1} b
    coeff = _dot(v, y) / denom
    return [yi - coeff * wi for yi, wi in zip(y, w)]


def sherman_morrison_solve_many(fact, u, v, bcols, *, sing_tol=SING_TOL,
                                stab_tol=STAB_TOL):
    """Rank-1 updated solve for multiple right-hand sides.

    The expensive parts (A^{-1} u and the denominator) are computed once
    and shared across all right-hand sides.
    """
    w, denom = _sherman_morrison_coeffs(fact, u, v, sing_tol, stab_tol,
                                        "sherman_morrison_solve_many")
    out = []
    for b in bcols:
        y = fact.solve(b)
        coeff = _dot(v, y) / denom
        out.append([yi - coeff * wi for yi, wi in zip(y, w)])
    return out


def sherman_morrison_reverse_solve(fact, u, v, b, *, sing_tol=SING_TOL,
                                   stab_tol=STAB_TOL):
    """Undo a rank-1 update: recover the solution of A x = b.

    Treats A as (A + u v^T) - u v^T and applies Sherman-Morrison to the
    updated operator, still using only the factorization of A.  Useful to
    verify that update -> reverse update round-trips the solution.
    """
    w, denom = _sherman_morrison_coeffs(fact, u, v, sing_tol, stab_tol,
                                        "sherman_morrison_reverse_solve")
    # z = (A + u v^T)^{-1} u = w / denom
    z = [wi / denom for wi in w]
    y = sherman_morrison_solve(fact, u, v, b, sing_tol=sing_tol,
                               stab_tol=stab_tol)  # y = (A + u v^T)^{-1} b
    denom_rev = 1.0 - _dot(v, z)  # = 1 / denom
    _check_denominator(denom_rev, sing_tol, stab_tol,
                       "sherman_morrison_reverse_solve (reverse)")
    coeff = _dot(v, y) / denom_rev
    return [yi + coeff * zi for yi, zi in zip(y, z)]


def woodbury_solve(fact, ucols, vcols, b, *, sing_tol=SING_TOL,
                   stab_tol=STAB_TOL):
    """Solve (A + U V^T) x = b for a rank-k update via Woodbury.

    ``ucols`` and ``vcols`` are lists of ``k`` dense column vectors of the
    factored size.  The k-by-k capacitance matrix C = I + V^T A^{-1} U is
    factored on the fly; if it is singular the update is rejected and the
    original factorization is left intact.
    """
    n = fact.n
    k = len(ucols)
    if k == 0:
        return fact.solve(b)
    if len(vcols) != k:
        raise ValueError("U and V must have the same number of columns")
    if any(len(col) != n for col in ucols + vcols):
        raise ValueError("update columns must match the factored size")
    zcols = [fact.solve(u) for u in ucols]  # Z = A^{-1} U, n-by-k
    # Capacitance matrix C = I + V^T Z (k-by-k).
    cap = [[(1.0 if i == j else 0.0) + _dot(vcols[i], zcols[j])
            for j in range(k)] for i in range(k)]
    try:
        cap_lu, cap_piv = lu_factor(cap)
    except SingularMatrixError as exc:
        raise SingularUpdateError(
            f"woodbury_solve: capacitance matrix is singular ({exc}); "
            "original factorization is unchanged"
        ) from exc
    # Instability: smallest pivot magnitude of the capacitance LU.
    min_piv = min(abs(cap_lu[i][i]) for i in range(k))
    if min_piv <= sing_tol:
        raise SingularUpdateError(
            f"woodbury_solve: capacitance pivot {min_piv:.3e} below "
            "tolerance; original factorization is unchanged"
        )
    if min_piv <= stab_tol:
        warnings.warn(
            f"woodbury_solve: smallest capacitance pivot {min_piv:.3e} is "
            "close to 0; update is numerically unstable",
            UnstableUpdateWarning,
            stacklevel=2,
        )
    y = fact.solve(b)  # y = A^{-1} b
    rhs = [_dot(vcols[i], y) for i in range(k)]  # V^T A^{-1} b
    coeffs = lu_solve(cap_lu, cap_piv, rhs)  # C^{-1} V^T A^{-1} b
    x = list(y)
    for j in range(k):
        cj = coeffs[j]
        if cj != 0.0:
            zj = zcols[j]
            for i in range(n):
                x[i] -= cj * zj[i]
    return x


def dense_reference_solve(a, b):
    """Direct dense solve of A x = b; reference for verification."""
    lu, piv = lu_factor(a)
    return lu_solve(lu, piv, b)


def _rank1_updated(a, u, v):
    return [[a[i][j] + u[i] * v[j] for j in range(len(a))]
            for i in range(len(a))]


def _demo():
    n = 4
    a = [
        [4.0, 1.0, 0.5, 0.0],
        [1.0, 3.0, 1.0, 0.25],
        [0.5, 1.0, 5.0, 1.0],
        [0.0, 0.25, 1.0, 2.0],
    ]
    b = [1.0, 2.0, 3.0, 4.0]
    fact = LUFactorization(a)

    x0 = fact.solve(b)
    print("original solve        x0 =", [f"{t:.6f}" for t in x0])

    # Rank-1 update with explicitly indexed sparse vectors.
    u = sparse_vector(n, indices=[0, 2], values=[1.5, -0.5])
    v = sparse_vector(n, indices=[1, 3], values=[0.75, 2.0])
    x1 = sherman_morrison_solve(fact, u, v, b)
    ref = dense_reference_solve(_rank1_updated(a, u, v), b)
    err = max(abs(p - q) for p, q in zip(x1, ref))
    print("rank-1 update         x1 =", [f"{t:.6f}" for t in x1])
    print(f"dense reference max|x1-ref| = {err:.3e}")

    xr = sherman_morrison_reverse_solve(fact, u, v, b)
    err = max(abs(p - q) for p, q in zip(xr, x0))
    print(f"reverse update max|xr-x0|   = {err:.3e}")

    # Multiple right-hand sides share one update computation.
    bcols = [b, [4.0, 3.0, 2.0, 1.0]]
    xs = sherman_morrison_solve_many(fact, u, v, bcols)
    refs = [dense_reference_solve(_rank1_updated(a, u, v), bc) for bc in bcols]
    err = max(abs(p - q) for col, rcol in zip(xs, refs)
              for p, q in zip(col, rcol))
    print(f"multi-RHS (2) max error     = {err:.3e}")

    # Rank-2 Woodbury update.
    ucols = [sparse_vector(n, [0], [1.0]), sparse_vector(n, [3], [0.5])]
    vcols = [sparse_vector(n, [1], [0.5]), sparse_vector(n, [2], [-1.0])]
    xw = woodbury_solve(fact, ucols, vcols, b)
    a2 = [row[:] for row in a]
    for uc, vc in zip(ucols, vcols):
        for i in range(n):
            for j in range(n):
                a2[i][j] += uc[i] * vc[j]
    ref = dense_reference_solve(a2, b)
    err = max(abs(p - q) for p, q in zip(xw, ref))
    print(f"rank-2 Woodbury max error   = {err:.3e}")

    # Singular update: choose v so that 1 + v^T A^{-1} u = 0.
    w = fact.solve(u)
    j = max(range(n), key=lambda i: abs(w[i]))
    v_bad = [0.0] * n
    v_bad[j] = -1.0 / w[j]
    try:
        sherman_morrison_solve(fact, u, v_bad, b)
    except SingularUpdateError as exc:
        print("singular update detected  ->", exc)
    x_ok = fact.solve(b)
    err = max(abs(p - q) for p, q in zip(x_ok, x0))
    print(f"factorization still usable, max|x-x0| = {err:.3e}")


if __name__ == "__main__":
    _demo()
