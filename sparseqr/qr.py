"""Rank-revealing QR least squares with column pivoting (Householder).

Solves min ||A x - b||_2 for a SparseMatrix A without forming normal
equations and without any matrix inversion. Column pivoting handles
near-collinear columns; a tolerance on the R diagonal yields a rank
estimate. The solution is permuted back to the original variable order.
"""

from __future__ import annotations

import math
import sys
from dataclasses import dataclass, field

from .sparse_matrix import SparseMatrix

__all__ = ["QRLSResult", "qr_least_squares"]


@dataclass
class QRLSResult:
    """Result of qr_least_squares.

    x: solution in the ORIGINAL variable order (length ncols).
    rank: estimated numerical rank of A.
    residual_norm: ||A x - b||_2.
    residuals: the vector A x - b (length nrows).
    column_permutation: perm[k] is the original index of the column used
        at factorization step k (AP = QR ordering).
    dependent_columns: original indices of columns treated as redundant
        (the tail of the permutation beyond the estimated rank).
    rank_deficient: True when rank < ncols.
    r_diag: diagonal of R in pivoted order, useful for inspecting the
        rank gap.
    """

    x: list
    rank: int
    residual_norm: float
    residuals: list
    column_permutation: list
    dependent_columns: list
    rank_deficient: bool
    r_diag: list = field(default_factory=list)


def _tail_norms_sq(cols, k):
    """Squared norms of the sub-columns restricted to rows >= k."""
    return [sum(v * v for i, v in col.items() if i >= k) for col in cols]


def _householder_vector(x, k):
    """Householder reflector for sparse subvector x (rows >= k).

    Returns (v, beta, alpha) with (I - beta v v^T) x = alpha e_k.
    """
    sigma = sum(value * value for value in x.values())
    if sigma == 0.0:
        return {}, 0.0, 0.0
    xk = x.get(k, 0.0)
    norm = math.sqrt(sigma)
    alpha = -math.copysign(norm, xk) if xk != 0.0 else -norm
    v = dict(x)
    v[k] = xk - alpha
    vv = sum(t * t for t in v.values())
    beta = 2.0 / vv
    return v, beta, alpha


def _apply_reflection(col, v, beta):
    """In-place: col <- (I - beta v v^T) col, preserving sparsity."""
    if beta == 0.0:
        return
    s = beta * sum(vi * col.get(i, 0.0) for i, vi in v.items())
    if s == 0.0:
        return
    for i, vi in v.items():
        new_value = col.get(i, 0.0) - s * vi
        if new_value == 0.0:
            col.pop(i, None)
        else:
            col[i] = new_value


def _qr_decompose(cols, nrows):
    """Householder QR with column pivoting on a list of column dicts.

    On return, cols[j] holds R column j (rows <= j) in pivoted order.
    Returns (perm, diag, reflectors).
    """
    n = len(cols)
    perm = list(range(n))
    diag = []
    reflectors = []
    for k in range(min(nrows, n)):
        norms_sq = _tail_norms_sq(cols, k)
        p = max(range(k, n), key=lambda j: norms_sq[j])
        if p != k:
            cols[k], cols[p] = cols[p], cols[k]
            perm[k], perm[p] = perm[p], perm[k]
        sub = {i: v for i, v in cols[k].items() if i >= k}
        v, beta, alpha = _householder_vector(sub, k)
        r_col = {i: val for i, val in cols[k].items() if i < k}
        if alpha != 0.0:
            r_col[k] = alpha
        cols[k] = r_col
        diag.append(alpha)
        reflectors.append((v, beta))
        for j in range(k + 1, n):
            _apply_reflection(cols[j], v, beta)
    return perm, diag, reflectors


def _apply_reflectors_to_vector(reflectors, vec):
    """Compute Q^T vec by replaying the stored Householder reflectors."""
    out = [float(value) for value in vec]
    for v, beta in reflectors:
        if beta == 0.0:
            continue
        s = beta * sum(vi * out[i] for i, vi in v.items())
        if s == 0.0:
            continue
        for i, vi in v.items():
            out[i] -= s * vi
    return out


def qr_least_squares(matrix, b, tol=None):
    """Solve min ||A x - b||_2 via rank-revealing QR with column pivoting.

    matrix: SparseMatrix; b: sequence of length matrix.nrows.
    tol: absolute threshold on |R[k][k]| for the rank estimate; defaults
        to max(m, n) * eps * max|R diag|. Columns beyond the rank get a
        zero coefficient (basic solution), which keeps near-collinear
        directions from blowing up.
    """
    if not isinstance(matrix, SparseMatrix):
        raise TypeError("matrix must be a SparseMatrix")
    if len(b) != matrix.nrows:
        raise ValueError(
            f"right-hand side of length {len(b)} does not match "
            f"{matrix.nrows} rows"
        )
    m, n = matrix.shape
    cols = matrix.columns()
    perm, diag, reflectors = _qr_decompose(cols, m)
    c = _apply_reflectors_to_vector(reflectors, b)

    max_diag = max((abs(d) for d in diag), default=0.0)
    if tol is None:
        tol = max(m, n) * sys.float_info.epsilon * max_diag
    rank = 0
    for d in diag:
        if abs(d) > tol:
            rank += 1
        else:
            break

    # Back substitution on the leading rank x rank triangle of R.
    x_perm = [0.0] * n
    for i in range(rank - 1, -1, -1):
        s = c[i]
        for j in range(i + 1, rank):
            rij = cols[j].get(i, 0.0)
            if rij != 0.0:
                s -= rij * x_perm[j]
        x_perm[i] = s / diag[i]

    # Undo the column permutation: x[perm[k]] = x_perm[k].
    x = [0.0] * n
    for k in range(n):
        x[perm[k]] = x_perm[k]

    residuals = matrix.matvec(x)
    for i in range(m):
        residuals[i] -= float(b[i])
    residual_norm = math.sqrt(sum(r * r for r in residuals))

    return QRLSResult(
        x=x,
        rank=rank,
        residual_norm=residual_norm,
        residuals=residuals,
        column_permutation=perm,
        dependent_columns=sorted(perm[rank:]),
        rank_deficient=rank < n,
        r_diag=diag,
    )
