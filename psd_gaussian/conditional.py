"""Conditional Gaussian distributions under linear equality constraints.

Given ``x ~ N(mu, Sigma)`` and constraints ``C x = d``, the conditional
distribution is Gaussian with

    mu_c    = mu + Sigma C^T (C Sigma C^T)^+ (d - C mu)
    Sigma_c = Sigma - Sigma C^T (C Sigma C^T)^+ C Sigma

where ``+`` denotes the tolerance-aware Moore-Penrose pseudoinverse (needed
because ``Sigma`` may be singular).  Two kinds of unsatisfiable conditions
are detected explicitly:

* ``inconsistent_constraints``: the linear system ``C x = d`` has no
  solution at all;
* ``outside_support``: the system is solvable but the event has probability
  zero, i.e. ``d - C mu`` is not in the range of ``C Sigma C^T``.

Zero-variance directions of the conditional covariance are reported so the
caller can see exactly which directions the constraints pin down.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from . import linalg
from .decomposition import Decomposition, PSDTolerance, decompose


class InfeasibleConditionError(ValueError):
    """Raised when the equality conditions cannot be satisfied."""

    def __init__(self, reason: str, residual: list[float] | None = None):
        self.reason = reason
        self.residual = residual
        detail = f"; residual = {residual}" if residual is not None else ""
        super().__init__(f"unsatisfiable condition ({reason}){detail}")


@dataclass
class ConditionalGaussian:
    """Conditional distribution ``x | C x = d`` ready for sampling."""

    mean: list[float]
    covariance: linalg.Matrix
    decomposition: Decomposition
    zero_variance_directions: list[list[float]] = field(default_factory=list)
    constraint_residual: float = 0.0


def _check_consistent(C: linalg.Matrix, d: list[float], scale_tol: float) -> None:
    """Gaussian elimination on [C | d]; raises if the system is inconsistent."""
    m = len(C)
    n = len(C[0])
    aug = [C[i][:] + [d[i]] for i in range(m)]
    row = 0
    for col in range(n):
        pivot = max(range(row, m), key=lambda r: abs(aug[r][col]), default=row)
        if row >= m or abs(aug[pivot][col]) <= scale_tol:
            continue
        aug[row], aug[pivot] = aug[pivot], aug[row]
        for r in range(row + 1, m):
            factor = aug[r][col] / aug[row][col]
            for c in range(col, n + 1):
                aug[r][c] -= factor * aug[row][c]
        row += 1
    for r in range(row, m):
        if abs(aug[r][n]) > scale_tol:
            raise InfeasibleConditionError(
                "inconsistent_constraints",
                residual=[aug[i][n] for i in range(m)],
            )


def condition_gaussian(
    mean,
    cov,
    constraints,
    values,
    *,
    tol: PSDTolerance | None = None,
) -> ConditionalGaussian:
    """Condition ``N(mean, cov)`` on ``constraints @ x == values``.

    ``cov`` may be a matrix or an existing :class:`Decomposition`.
    """
    tol = tol or PSDTolerance()
    mu = [float(x) for x in mean]
    decomp = cov if isinstance(cov, Decomposition) else decompose(cov, tol)
    C = linalg.as_matrix(constraints, name="constraints")
    d = [float(x) for x in values]
    m = len(C)
    n = len(mu)
    if len(d) != m:
        raise ValueError("len(values) must match number of constraint rows")
    if any(len(row) != n for row in C):
        raise ValueError("constraint rows must have dimension len(mean)")

    c_scale = max(1.0, linalg.max_abs(C))
    _check_consistent(C, d, tol.threshold(c_scale))

    A = decomp.factor  # n x k
    sigma = linalg.matmul(A, linalg.transpose(A))
    B = linalg.matmul(C, A)  # m x k
    M = linalg.matmul(B, linalg.transpose(B))  # m x m = C Sigma C^T

    evals, evecs = linalg.jacobi_eigen(M)
    m_scale = max(1.0, max((abs(e) for e in evals), default=0.0))
    threshold = tol.threshold(m_scale)
    # Pseudoinverse of M from its spectrum; zero eigenvalues stay zero.
    M_inv = [[0.0] * m for _ in range(m)]
    for value, vector in zip(evals, evecs):
        if value > threshold:
            inv = 1.0 / value
            for i in range(m):
                for j in range(m):
                    M_inv[i][j] += inv * vector[i] * vector[j]

    r = [d[i] - sum(C[i][j] * mu[j] for j in range(n)) for i in range(m)]
    M_inv_r = linalg.matvec(M_inv, r)
    proj_residual = linalg.sub([r], [linalg.matvec(M, M_inv_r)])[0]
    res_norm = linalg.norm2(proj_residual)
    feas_tol = tol.threshold(max(1.0, linalg.norm2(r), linalg.norm2(d)))
    if res_norm > feas_tol:
        raise InfeasibleConditionError("outside_support", residual=proj_residual)

    K = linalg.matmul(linalg.matmul(sigma, linalg.transpose(C)), M_inv)  # n x m
    mu_c = [mu[i] + sum(K[i][j] * r[j] for j in range(m)) for i in range(n)]
    KB = linalg.matmul(K, B)  # n x k
    sigma_c = linalg.sub(sigma, linalg.matmul(KB, linalg.transpose(KB)))
    sigma_c = linalg.symmetrize(sigma_c)
    decomp_c = decompose(sigma_c, tol)

    zero_dirs = [
        vector
        for value, vector in zip(decomp_c.eigenvalues, decomp_c.eigenvectors)
        if abs(value) <= threshold
    ]
    return ConditionalGaussian(
        mean=mu_c,
        covariance=sigma_c,
        decomposition=decomp_c,
        zero_variance_directions=zero_dirs,
        constraint_residual=res_norm,
    )
