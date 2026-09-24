"""Dense linear-algebra primitives used by the incremental solver.

Pure standard library; matrices are lists of row lists.  The Cholesky
rank-one update/downdate recurrences follow the classical LINPACK
formulation (positive diagonal kept throughout).
"""

import math


class RankDropError(Exception):
    """A Cholesky (down)date would destroy positive definiteness."""

    def __init__(self, step, diagonal):
        super().__init__(
            "positive definiteness lost at step %d (residual diagonal %.3e)"
            % (step, diagonal)
        )
        self.step = step
        self.diagonal = diagonal


def cholesky_factor(matrix, tol=0.0):
    """Return the lower-triangular L with L L^T = matrix.

    Raises RankDropError if a pivot is not strictly above ``tol``.
    """
    n = len(matrix)
    L = [[0.0] * n for _ in range(n)]
    for i in range(n):
        for j in range(i + 1):
            acc = matrix[i][j]
            for k in range(j):
                acc -= L[i][k] * L[j][k]
            if i == j:
                if not math.isfinite(acc) or acc <= tol:
                    raise RankDropError(i, acc)
                L[i][j] = math.sqrt(acc)
            else:
                L[i][j] = acc / L[j][j]
    return L


def cholesky_rank1(L, x, sign, rank_tol=1e-12):
    """In-place rank-one update of a lower Cholesky factor.

    sign = +1:  L L^T  ->  L L^T + x x^T   (update)
    sign = -1:  L L^T  ->  L L^T - x x^T   (downdate)

    Raises RankDropError if a downdate pivot drops to or below
    ``rank_tol`` times its previous squared value, i.e. the result
    would be singular or numerically indefinite.  On raise, ``L`` is
    left partially modified; callers must work on a copy if they need
    atomicity.
    """
    n = len(L)
    work = list(x)
    for k in range(n):
        lkk = L[k][k]
        diag = lkk * lkk + sign * work[k] * work[k]
        if diag <= rank_tol * lkk * lkk:
            raise RankDropError(k, diag)
        root = math.sqrt(diag)
        c = root / lkk
        s = work[k] / lkk
        L[k][k] = root
        for i in range(k + 1, n):
            updated = (L[i][k] + sign * s * work[i]) / c
            work[i] = c * work[i] - s * updated
            L[i][k] = updated
    return L


def forward_substitution(L, b):
    """Solve L y = b for lower-triangular L."""
    n = len(L)
    y = [0.0] * n
    for i in range(n):
        acc = b[i]
        row = L[i]
        for k in range(i):
            acc -= row[k] * y[k]
        y[i] = acc / row[i]
    return y


def back_substitution_transpose(L, b):
    """Solve L^T x = b for lower-triangular L."""
    n = len(L)
    x = [0.0] * n
    for i in range(n - 1, -1, -1):
        acc = b[i]
        for k in range(i + 1, n):
            acc -= L[k][i] * x[k]
        x[i] = acc / L[i][i]
    return x


def cholesky_solve(L, b):
    """Solve (L L^T) x = b via two triangular solves."""
    return back_substitution_transpose(L, forward_substitution(L, b))


def llt(L):
    """Return the full product L L^T."""
    n = len(L)
    out = [[0.0] * n for _ in range(n)]
    for i in range(n):
        for j in range(i + 1):
            acc = 0.0
            for k in range(j + 1):
                acc += L[i][k] * L[j][k]
            out[i][j] = acc
            out[j][i] = acc
    return out


def mat_vec(matrix, x):
    return [sum(mi * xi for mi, xi in zip(row, x)) for row in matrix]


def reduced_row_echelon(matrix, tol):
    """RREF with partial pivoting; returns (rref, pivot_columns)."""
    a = [list(row) for row in matrix]
    n_rows = len(a)
    n_cols = len(a[0])
    pivots = []
    r = 0
    for c in range(n_cols):
        if r == n_rows:
            break
        pivot = max(range(r, n_rows), key=lambda i: abs(a[i][c]))
        if abs(a[pivot][c]) <= tol:
            continue
        a[r], a[pivot] = a[pivot], a[r]
        inv = 1.0 / a[r][c]
        a[r] = [v * inv for v in a[r]]
        for i in range(n_rows):
            if i != r:
                factor = a[i][c]
                if factor != 0.0:
                    a[i] = [v - factor * w for v, w in zip(a[i], a[r])]
        pivots.append(c)
        r += 1
    return a, pivots


def dense_rank(matrix, rel_tol=1e-9):
    """Numerical rank via RREF pivot count."""
    scale = max((abs(v) for row in matrix for v in row), default=0.0)
    _, pivots = reduced_row_echelon(matrix, rel_tol * max(scale, 1.0))
    return len(pivots)


def solution_space(M, z, rel_tol=1e-9):
    """Solve the (possibly rank-deficient) symmetric system M x = z.

    Returns (particular, null_basis, rank): one particular solution and
    a basis of the null space of M, so the full solution set is
    ``particular + span(null_basis)``.
    """
    n = len(M)
    scale = max((abs(v) for row in M for v in row), default=0.0)
    tol = rel_tol * max(scale, 1.0)
    aug = [list(M[i]) + [z[i]] for i in range(n)]
    rref, pivots = reduced_row_echelon(aug, tol)
    pivots = [c for c in pivots if c < n]
    rank = len(pivots)
    particular = [0.0] * n
    for i, c in enumerate(pivots):
        particular[c] = rref[i][n]
    pivot_set = set(pivots)
    basis = []
    for f in range(n):
        if f in pivot_set:
            continue
        v = [0.0] * n
        v[f] = 1.0
        for i, c in enumerate(pivots):
            v[c] = -rref[i][f]
        basis.append(v)
    return particular, basis, rank
