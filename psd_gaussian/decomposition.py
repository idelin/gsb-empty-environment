"""Spectral decomposition of symmetric positive-semidefinite (PSD) matrices.

Design rules (per project requirements):

* PSD-ness is judged against a *configurable* tolerance (:class:`PSDTolerance`).
* A matrix whose smallest eigenvalue falls below ``-threshold`` is rejected
  with a *witness* direction ``v`` such that ``v^T Sigma v < 0``.
* Near-singular matrices are handled by an exact rank-revealing spectral
  factor: eigenvalues in the tolerance band ``[-threshold, 0)`` are truncated
  to zero and *reported*; no jitter is ever added to the matrix, and no
  positive eigenvalue is dropped or inflated.
* Negative eigenvalues are never square-rooted; rejection happens first.
"""
from __future__ import annotations

import math
from dataclasses import dataclass, field

from . import linalg


@dataclass(frozen=True)
class PSDTolerance:
    """Configurable numerical criteria for PSD determination.

    An eigenvalue ``lam`` of a matrix with spectral scale ``s`` is
    * accepted as zero when ``-threshold <= lam <= 0``,
    * a hard failure when ``lam < -threshold``,
    where ``threshold = atol + rtol * s`` and
    ``s = max(1, max |eigenvalue|)``.
    """

    atol: float = 1e-10
    rtol: float = 1e-10

    def threshold(self, scale: float) -> float:
        return self.atol + self.rtol * max(1.0, scale)


class NotSymmetricError(ValueError):
    """Raised when the input matrix is not symmetric within tolerance."""


class NotPositiveSemidefiniteError(ValueError):
    """Raised when the matrix has an eigenvalue below ``-threshold``.

    Carries the offending eigenvalue and a witness vector ``v`` with
    ``v^T Sigma v < 0`` so the caller can inspect the negative direction.
    """

    def __init__(self, eigenvalue: float, witness: list[float], witness_value: float):
        self.eigenvalue = eigenvalue
        self.witness = witness
        self.witness_value = witness_value
        super().__init__(
            "matrix is not positive semidefinite: "
            f"eigenvalue {eigenvalue:.6e} with witness v (v^T Sigma v = "
            f"{witness_value:.6e})"
        )


@dataclass
class Decomposition:
    """Result of :func:`decompose`.

    ``factor`` is an ``n x k`` matrix ``A`` with ``A A^T ≈ Sigma``; columns are
    ``sqrt(lam_i) * v_i`` for every strictly positive eigenvalue.  Nothing is
    smoothed or jittered, so the residual fields below describe the exact
    (deterministic) approximation error of the factorization.
    """

    factor: linalg.Matrix
    eigenvalues: list[float]
    eigenvectors: list[list[float]]
    rank: int
    residual_fro: float
    residual_max: float
    truncated_eigenvalues: list[float] = field(default_factory=list)
    tolerance: PSDTolerance = field(default_factory=PSDTolerance)

    def reconstruct(self) -> linalg.Matrix:
        """Return ``A A^T`` (the matrix actually sampled from)."""
        return linalg.matmul(self.factor, linalg.transpose(self.factor))


def decompose(cov, tol: PSDTolerance | None = None) -> Decomposition:
    """Factor a symmetric PSD matrix as ``A A^T`` via its eigendecomposition.

    Raises :class:`NotSymmetricError` for asymmetric input and
    :class:`NotPositiveSemidefiniteError` (with witness) for matrices with an
    eigenvalue below the negative tolerance band.
    """
    tol = tol or PSDTolerance()
    a = linalg.as_matrix(cov, name="covariance")
    n = len(a)
    if any(len(row) != n for row in a):
        raise ValueError("covariance matrix must be square")
    scale_in = max(1.0, linalg.max_abs(a))
    if linalg.asymmetry(a) > tol.threshold(scale_in):
        raise NotSymmetricError("covariance matrix is not symmetric within tolerance")
    sym = linalg.symmetrize(a)
    values, vectors = linalg.jacobi_eigen(sym)
    scale = max(1.0, max(abs(v) for v in values))
    threshold = tol.threshold(scale)

    min_value = values[-1]
    if min_value < -threshold:
        witness = vectors[-1]
        raise NotPositiveSemidefiniteError(
            min_value, witness, linalg.quadratic_form(witness, sym)
        )

    truncated = [v for v in values if v < 0.0]
    columns = []
    for value, vector in zip(values, vectors):
        if value > 0.0:
            root = math.sqrt(value)  # only ever applied to positive eigenvalues
            columns.append([root * x for x in vector])
    factor = linalg.transpose(columns) if columns else [[] for _ in range(n)]

    residual = linalg.sub(
        sym, linalg.matmul(factor, linalg.transpose(factor))
    )
    rank = sum(1 for v in values if v > threshold)
    return Decomposition(
        factor=factor,
        eigenvalues=values,
        eigenvectors=vectors,
        rank=rank,
        residual_fro=linalg.frobenius(residual),
        residual_max=linalg.max_abs(residual),
        truncated_eigenvalues=truncated,
        tolerance=tol,
    )
