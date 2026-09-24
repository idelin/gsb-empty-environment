"""QR least-squares solver for small/medium sparse matrices.

Pure Python 3.11 standard library.  The matrix is supplied by explicit
dimensions and explicit (row, col, value) triples; the solver uses a
column-pivoted Householder QR factorization and back substitution -- it
never forms an explicit inverse and never uses the normal equations.

Column pivoting handles (near-)dependent columns: at every step the
remaining column of largest tail norm is eliminated next, and the rank
estimate is the number of pivots accepted before the remaining columns
fall below a tolerance.  The solution is always returned in the original
variable order, regardless of the internal column permutation.
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field

_MACHINE_EPS = 2.220446049250313e-16


class SparseMatrix:
    """Sparse matrix given by explicit size and (row, col, value) entries.

    Stored internally as a list of per-column dicts {row: value}.
    Duplicate triples are summed; explicit zeros are dropped.
    """

    def __init__(self, nrows: int, ncols: int, entries):
        nrows, ncols = int(nrows), int(ncols)
        if nrows <= 0 or ncols <= 0:
            raise ValueError("matrix dimensions must be positive")
        self.nrows = nrows
        self.ncols = ncols
        cols: list[dict[int, float]] = [dict() for _ in range(ncols)]
        for (i, j, value) in entries:
            if not (0 <= i < nrows) or not (0 <= j < ncols):
                raise IndexError(f"entry ({i}, {j}) out of {nrows}x{ncols} range")
            cols[j][i] = cols[j].get(i, 0.0) + float(value)
        self._cols = [{i: v for i, v in col.items() if v != 0.0} for col in cols]

    def columns(self) -> list[dict[int, float]]:
        """Return a mutable copy of the column storage."""
        return [dict(col) for col in self._cols]

    def matvec(self, x: list[float]) -> list[float]:
        """Return A @ x for a dense vector x of length ncols."""
        if len(x) != self.ncols:
            raise ValueError("vector length does not match ncols")
        out = [0.0] * self.nrows
        for j, col in enumerate(self._cols):
            xj = x[j]
            if xj == 0.0:
                continue
            for i, v in col.items():
                out[i] += v * xj
        return out


@dataclass
class LeastSquaresResult:
    """Result of a column-pivoted QR least-squares solve.

    Attributes:
        x: least-squares solution in the ORIGINAL variable order.
        rank: numerical rank estimate of A.
        residual: residual vector b - A @ x in the original row order.
        residual_norm: Euclidean norm of the residual.
        rank_deficient: True when rank < ncols (e.g. duplicate columns).
        dependent_columns: original indices of columns not used as pivots
            (the columns responsible for the rank deficiency).
        column_permutation: column_permutation[k] is the original index of
            the column eliminated at step k (length == rank).
        r_diag: diagonal of the triangular factor R (length == rank).
    """

    x: list[float]
    rank: int
    residual: list[float]
    residual_norm: float
    rank_deficient: bool
    dependent_columns: list[int] = field(default_factory=list)
    column_permutation: list[int] = field(default_factory=list)
    r_diag: list[float] = field(default_factory=list)


def qr_least_squares(A: SparseMatrix, b, tol: float | None = None) -> LeastSquaresResult:
    """Solve min_x ||A x - b||_2 via column-pivoted Householder QR.

    Args:
        A: SparseMatrix with explicit dimensions and entries.
        b: right-hand side, a sequence of length A.nrows.
        tol: absolute pivot tolerance; columns whose remaining tail norm
            falls below it are treated as dependent.  Defaults to
            max(m, n) * eps * max_initial_column_norm.

    Returns:
        LeastSquaresResult with the solution restored to the original
        variable order, the rank estimate and the residual.
    """
    m, n = A.nrows, A.ncols
    b = [float(v) for v in b]
    if len(b) != m:
        raise ValueError("right-hand side length does not match nrows")

    cols = A.columns()
    perm = list(range(n))
    scale = max((math.sqrt(sum(v * v for v in col.values())) for col in cols),
                default=0.0)
    if tol is None:
        tol = max(m, n) * _MACHINE_EPS * scale

    householders: list[tuple[dict[int, float], float]] = []
    r_rows: list[dict[int, float]] = []   # strict upper part of R, per row
    r_diag: list[float] = []
    rank = 0

    for k in range(min(m, n)):
        # Tail norms (rows >= k) of the remaining columns.
        tail = [0.0] * n
        for j in range(k, n):
            tail[j] = math.sqrt(sum(v * v for i, v in cols[j].items() if i >= k))
        p = max(range(k, n), key=lambda j: tail[j])
        if tail[p] <= tol:
            break  # all remaining columns negligible -> rank = k

        # Column pivot: swap columns k and p.
        cols[k], cols[p] = cols[p], cols[k]
        perm[k], perm[p] = perm[p], perm[k]

        col = cols[k]
        xk = col.get(k, 0.0)
        alpha = -math.copysign(tail[p], xk) if xk != 0.0 else -tail[p]

        # Householder vector v = x - alpha*e_k on rows >= k.
        v = {i: val for i, val in col.items() if i > k}
        v[k] = xk - alpha
        beta = 2.0 / sum(vi * vi for vi in v.values())
        householders.append((v, beta))
        r_diag.append(alpha)
        for i in range(k):
            r_rows[i][k] = col.get(i, 0.0)
        r_rows.append({})

        # Apply H = I - beta v v^T to the remaining columns.
        for j in range(k + 1, n):
            c = cols[j]
            s = beta * sum(vi * c.get(i, 0.0) for i, vi in v.items())
            if s == 0.0:
                continue
            for i, vi in v.items():
                nv = c.get(i, 0.0) - s * vi
                if nv != 0.0:
                    c[i] = nv
                else:
                    c.pop(i, None)
        rank = k + 1

    # Q^T b via the stored reflectors.
    qb = list(b)
    for v, beta in householders:
        s = beta * sum(vi * qb[i] for i, vi in v.items())
        for i, vi in v.items():
            qb[i] -= s * vi

    # Back substitution on the rank x rank upper triangle (no inversion).
    z = [0.0] * rank
    for k in range(rank - 1, -1, -1):
        acc = qb[k]
        for j, rv in r_rows[k].items():
            if j > k:
                acc -= rv * z[j]
        z[k] = acc / r_diag[k]

    # Undo the column permutation: solution in original variable order.
    x = [0.0] * n
    for k in range(rank):
        x[perm[k]] = z[k]

    ax = A.matvec(x)
    residual = [bi - ai for bi, ai in zip(b, ax)]
    residual_norm = math.sqrt(sum(r * r for r in residual))

    return LeastSquaresResult(
        x=x,
        rank=rank,
        residual=residual,
        residual_norm=residual_norm,
        rank_deficient=rank < n,
        dependent_columns=sorted(perm[rank:]),
        column_permutation=perm[:rank],
        r_diag=r_diag,
    )


def _demo() -> None:
    print("=== Example 1: overdetermined 5x3 least squares (quadratic fit) ===")
    entries = []
    for row in range(5):
        t = float(row)
        entries += [(row, 0, 1.0), (row, 1, t), (row, 2, t * t)]
    A = SparseMatrix(5, 3, entries)
    b = [1.0, 2.0, 0.0, 1.0, 5.0]
    res = qr_least_squares(A, b)
    print("x      =", [round(v, 10) for v in res.x])
    print("rank   =", res.rank, " rank_deficient =", res.rank_deficient)
    print("||r||  =", res.residual_norm)

    print("\n=== Example 2: duplicate columns -> rank deficiency reported ===")
    # Column 2 is an exact copy of column 0.
    A = SparseMatrix(4, 3, [
        (0, 0, 1.0), (1, 0, 2.0), (2, 0, 3.0), (3, 0, 4.0),
        (0, 1, 4.0), (1, 1, 3.0), (2, 1, 2.0), (3, 1, 1.0),
        (0, 2, 1.0), (1, 2, 2.0), (2, 2, 3.0), (3, 2, 4.0),
    ])
    res = qr_least_squares(A, [1.0, 1.0, 1.0, 1.0])
    print("x      =", [round(v, 10) for v in res.x])
    print("rank   =", res.rank, " rank_deficient =", res.rank_deficient)
    print("dependent columns =", res.dependent_columns)
    print("||r||  =", res.residual_norm)

    print("\n=== Example 3: column pivoting, solution in original order ===")
    # Column norms differ by 1e4, forcing a nontrivial pivot order.
    A = SparseMatrix(3, 3, [
        (0, 0, 1e-4), (1, 0, 0.0), (2, 0, 1e-4),
        (0, 1, 1.0), (1, 1, 1.0), (2, 1, 0.0),
        (0, 2, 0.0), (1, 2, 1.0), (2, 2, 1.0),
    ])
    b = [1.0, 2.0, 3.0]
    res = qr_least_squares(A, b)
    print("pivot order (original indices) =", res.column_permutation)
    print("x (original order) =", [round(v, 6) for v in res.x])
    print("A @ x - b =", [f"{v:.2e}" for v in
                          [a - bi for a, bi in zip(A.matvec(res.x), b)]])


if __name__ == "__main__":
    _demo()
