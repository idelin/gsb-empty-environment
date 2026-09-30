"""Exact rational linear algebra over ``fractions.Fraction``.

All solves are exact: a returned residual of ``Fraction(0)`` is a proof,
not a floating-point approximation.  This module deliberately avoids any
tolerance-based reasoning so that "small change" is never confused with
"converged".
"""

from fractions import Fraction


def F(value):
    """Coerce to Fraction (int/Fraction/str-safe)."""
    if isinstance(value, Fraction):
        return value
    return Fraction(value)


def rref(matrix, pivot_col_limit=None):
    """Reduced row echelon form over the rationals.

    Returns ``(R, pivot_columns)`` where pivot rows occupy the top of ``R``
    in pivot order.  Only columns with index < ``pivot_col_limit`` are used
    as pivot candidates (default: all columns); remaining columns are still
    eliminated against.
    """
    mat = [[F(v) for v in row] for row in matrix]
    nrows = len(mat)
    ncols = len(mat[0]) if nrows else 0
    limit = ncols if pivot_col_limit is None else pivot_col_limit
    pivots = []
    r = 0
    for c in range(limit):
        piv = None
        for i in range(r, nrows):
            if mat[i][c] != 0:
                piv = i
                break
        if piv is None:
            continue
        mat[r], mat[piv] = mat[piv], mat[r]
        inv = 1 / mat[r][c]
        mat[r] = [v * inv for v in mat[r]]
        for i in range(nrows):
            if i != r and mat[i][c] != 0:
                factor = mat[i][c]
                mat[i] = [a - factor * b for a, b in zip(mat[i], mat[r])]
        pivots.append(c)
        r += 1
        if r == nrows:
            break
    return mat, pivots


def rank(matrix):
    if not matrix:
        return 0
    _, pivots = rref(matrix)
    return len(pivots)


def nullspace(A):
    """Basis for ``{x : A x = 0}`` over the rationals (list of vectors)."""
    if not A:
        return []
    ncols = len(A[0])
    R, pivots = rref(A)
    pivot_set = set(pivots)
    free_cols = [c for c in range(ncols) if c not in pivot_set]
    basis = []
    for free in free_cols:
        vec = [Fraction(0)] * ncols
        vec[free] = Fraction(1)
        for row_idx, pc in enumerate(pivots):
            vec[pc] = -R[row_idx][free]
        basis.append(vec)
    return basis


def solve_general(A, b):
    """Solve ``A x = b`` over the rationals.

    Returns ``(particular, null_basis)`` where every solution is
    ``particular + span(null_basis)``, or ``None`` if inconsistent.
    """
    m = len(A)
    n = len(A[0]) if m else 0
    aug = [list(A[i]) + [F(b[i])] for i in range(m)]
    R, pivots = rref(aug, pivot_col_limit=n)
    for i in range(len(pivots), m):
        if R[i][n] != 0:
            return None
    particular = [Fraction(0)] * n
    for row_idx, pc in enumerate(pivots):
        particular[pc] = R[row_idx][n]
    return particular, nullspace(A)


def independent_row_indices(A):
    """Indices of a maximal set of linearly independent rows of ``A``."""
    if not A:
        return []
    ncols = len(A[0])
    nrows = len(A)
    transposed = [[A[i][j] for i in range(nrows)] for j in range(ncols)]
    _, pivots = rref(transposed)
    return sorted(pivots)


def inconsistency_certificate(A, b):
    """Farkas-style certificate for ``A x = b`` being inconsistent.

    Returns a vector ``mu`` with ``mu^T A = 0`` and ``mu^T b != 0``,
    or ``None`` if the system is consistent.  Exact: verifying the
    certificate costs two exact dot products.
    """
    m = len(A)
    n = len(A[0]) if m else 0
    aug = []
    for i in range(m):
        row = list(A[i]) + [F(b[i])]
        row += [Fraction(1) if j == i else Fraction(0) for j in range(m)]
        aug.append(row)
    R, pivots = rref(aug, pivot_col_limit=n)
    for i in range(len(pivots), m):
        if R[i][n] != 0:
            return R[i][n + 1:]
    return None


def dot(u, v):
    return sum((a * b for a, b in zip(u, v)), Fraction(0))
