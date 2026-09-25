"""Self-written dense linear algebra (no NumPy/SciPy).

The routines are generic over the numeric type: they work unchanged for
``float`` and for ``fractions.Fraction`` (exact rational arithmetic).
"""

from .errors import SingularMatrixError


def lu_factorize(a):
    """LU factorization with partial pivoting.

    Returns ``(lu, perm)`` where ``lu`` packs L (unit diagonal, below) and U
    (diagonal and above), and ``perm`` is the row permutation such that
    ``A[perm[i]] == (L U)[i]``.
    """
    n = len(a)
    lu = [list(row) for row in a]
    perm = list(range(n))
    for k in range(n):
        pivot = max(range(k, n), key=lambda r: abs(lu[r][k]))
        if lu[pivot][k] == 0:
            raise SingularMatrixError(f"matrix is singular at column {k}")
        if pivot != k:
            lu[k], lu[pivot] = lu[pivot], lu[k]
            perm[k], perm[pivot] = perm[pivot], perm[k]
        for i in range(k + 1, n):
            lu[i][k] /= lu[k][k]
            factor = lu[i][k]
            if factor == 0:
                continue
            row_i = lu[i]
            row_k = lu[k]
            for j in range(k + 1, n):
                row_i[j] -= factor * row_k[j]
    return lu, perm


def lu_solve(lu, perm, b):
    """Solve ``A x = b`` given the factorization from :func:`lu_factorize`."""
    n = len(lu)
    x = [b[perm[i]] for i in range(n)]
    for i in range(1, n):
        s = x[i]
        row = lu[i]
        for j in range(i):
            s -= row[j] * x[j]
        x[i] = s
    for i in range(n - 1, -1, -1):
        s = x[i]
        row = lu[i]
        for j in range(i + 1, n):
            s -= row[j] * x[j]
        x[i] = s / row[i]
    return x


def residual_vector(a, x, b):
    """``b - A x`` as a list."""
    n = len(a)
    r = []
    for i in range(n):
        s = 0
        row = a[i]
        for j in range(n):
            s += row[j] * x[j]
        r.append(b[i] - s)
    return r


def dense_residual(a, x, b):
    """Infinity norm of ``b - A x``."""
    worst = 0
    for ri in residual_vector(a, x, b):
        if abs(ri) > worst:
            worst = abs(ri)
    return worst
