"""Pairwise-complete covariance over sparse observations.

Design rules
------------
* Missing values (``None``) are never treated as zero; a pair (i, j) only
  uses rows where *both* variables are observed (pairwise-complete).
* Every pair maintains its own mergeable central-moment statistics
  (count, mean_x, mean_y, comoment) using the Welford/Chan parallel
  algorithm.  The naive "E[x^2] - E[x]^2" form is never used, so huge
  means with tiny variances do not lose precision to cancellation.
* Each batch is stored as retraction evidence under an explicit batch id.
  Adding the same id twice is an idempotent no-op; removing an unknown id
  raises ``KeyError``.  Counts can never go negative.
* The denominator rule is explicit: ``ddof=1`` (sample covariance) by
  default; a pair with ``count <= ddof`` is *undefined* (``None``), never
  silently clamped.
* Pairwise-complete covariance matrices are not guaranteed to be positive
  semidefinite.  ``check_psd`` reports a counterexample vector ``v`` with
  ``v^T C v < 0`` (or a diagnostic listing undefined pairs).  The matrix
  itself is never modified, shrunk, or "repaired".

Pure standard library; no NumPy/SciPy.  Python 3.11+.
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field
from typing import Iterable, Optional, Sequence

__all__ = [
    "PairStats",
    "PairwiseCovariance",
    "PSDDiagnostic",
    "merge_stats",
    "subtract_stats",
    "quadratic_form",
]


# ---------------------------------------------------------------------------
# Mergeable per-pair central-moment statistics (Welford / Chan)
# ---------------------------------------------------------------------------


@dataclass
class PairStats:
    """Sufficient statistics for one variable pair.

    ``comoment`` is sum((x - mean_x) * (y - mean_y)) over the ``count``
    rows where both variables are observed.
    """

    count: int = 0
    mean_x: float = 0.0
    mean_y: float = 0.0
    comoment: float = 0.0

    def update(self, x: float, y: float) -> None:
        """Welford one-row update (numerically stable, no sum-of-squares)."""
        self.count += 1
        dx = x - self.mean_x
        self.mean_x += dx / self.count
        self.mean_y += (y - self.mean_y) / self.count
        # C_n = C_{n-1} + (x - mean_x_{n-1}) * (y - mean_y_n)
        self.comoment += dx * (y - self.mean_y)

    def copy(self) -> "PairStats":
        return PairStats(self.count, self.mean_x, self.mean_y, self.comoment)


def merge_stats(a: PairStats, b: PairStats) -> PairStats:
    """Chan et al. parallel combination of two pair statistics."""
    if b.count == 0:
        return a.copy()
    if a.count == 0:
        return b.copy()
    count = a.count + b.count
    dx = b.mean_x - a.mean_x
    dy = b.mean_y - a.mean_y
    return PairStats(
        count=count,
        mean_x=a.mean_x + dx * b.count / count,
        mean_y=a.mean_y + dy * b.count / count,
        comoment=a.comoment + b.comoment + dx * dy * (a.count * b.count) / count,
    )


def subtract_stats(total: PairStats, part: PairStats) -> PairStats:
    """Exact inverse of :func:`merge_stats`: remove ``part`` from ``total``.

    Raises ``ValueError`` if the retraction would produce a negative count.
    """
    remaining = total.count - part.count
    if remaining < 0:
        raise ValueError(
            f"retraction would make count negative: {total.count} - {part.count}"
        )
    if part.count == 0:
        return total.copy()
    if remaining == 0:
        return PairStats()
    dx = part.mean_x - total.mean_x
    dy = part.mean_y - total.mean_y
    return PairStats(
        count=remaining,
        mean_x=(total.count * total.mean_x - part.count * part.mean_x) / remaining,
        mean_y=(total.count * total.mean_y - part.count * part.mean_y) / remaining,
        comoment=total.comoment
        - part.comoment
        - dx * dy * (total.count * part.count) / remaining,
    )


# ---------------------------------------------------------------------------
# Small dense linear algebra (self-written, for PSD diagnostics)
# ---------------------------------------------------------------------------


def quadratic_form(matrix: Sequence[Sequence[float]], v: Sequence[float]) -> float:
    """Compute v^T M v for a dense symmetric matrix."""
    n = len(v)
    return sum(v[i] * matrix[i][j] * v[j] for i in range(n) for j in range(n))


def _jacobi_eigendecomposition(
    matrix: Sequence[Sequence[float]],
    *,
    tol: float = 1e-12,
    max_sweeps: int = 100,
) -> tuple[list[float], list[list[float]]]:
    """Eigenvalues/eigenvectors of a small real symmetric matrix.

    Cyclic Jacobi rotations.  Returns ``(eigenvalues, eigenvectors)`` where
    ``eigenvectors[k]`` is the (unit) eigenvector for ``eigenvalues[k]``.
    """
    n = len(matrix)
    a = [list(row) for row in matrix]
    vecs = [[1.0 if i == j else 0.0 for j in range(n)] for i in range(n)]

    for _ in range(max_sweeps):
        off = math.sqrt(
            sum(a[p][q] ** 2 for p in range(n) for q in range(n) if p != q)
        )
        if off <= tol:
            break
        for p in range(n - 1):
            for q in range(p + 1, n):
                if abs(a[p][q]) <= tol * 1e-3:
                    continue
                theta = (a[q][q] - a[p][p]) / (2.0 * a[p][q])
                if theta == 0.0:
                    t = 1.0
                else:
                    t = math.copysign(1.0, theta) / (
                        abs(theta) + math.sqrt(theta * theta + 1.0)
                    )
                c = 1.0 / math.sqrt(t * t + 1.0)
                s = t * c
                for k in range(n):
                    akp, akq = a[k][p], a[k][q]
                    a[k][p] = c * akp - s * akq
                    a[k][q] = s * akp + c * akq
                for k in range(n):
                    apk, aqk = a[p][k], a[q][k]
                    a[p][k] = c * apk - s * aqk
                    a[q][k] = s * apk + c * aqk
                for k in range(n):
                    vkp, vkq = vecs[k][p], vecs[k][q]
                    vecs[k][p] = c * vkp - s * vkq
                    vecs[k][q] = s * vkp + c * vkq

    eigenvalues = [a[i][i] for i in range(n)]
    eigenvectors = [[vecs[i][k] for i in range(n)] for k in range(n)]
    return eigenvalues, eigenvectors


# ---------------------------------------------------------------------------
# PSD diagnostic result
# ---------------------------------------------------------------------------


@dataclass
class PSDDiagnostic:
    """Result of :meth:`PairwiseCovariance.check_psd`.

    ``status`` is one of:

    * ``"undefined_pairs"`` -- some pairs have too few common observations;
      the matrix has holes and PSD is not decidable.  ``undefined_pairs``
      lists them.
    * ``"psd"`` -- smallest eigenvalue >= -``tol``.
    * ``"not_psd"`` -- ``counterexample`` is a vector ``v`` with
      ``v^T C v < 0`` (the eigenvector of the most negative eigenvalue).
    """

    status: str
    min_eigenvalue: Optional[float] = None
    counterexample: Optional[list[float]] = None
    undefined_pairs: list[tuple[int, int]] = field(default_factory=list)
    message: str = ""


# ---------------------------------------------------------------------------
# Main accumulator
# ---------------------------------------------------------------------------


class PairwiseCovariance:
    """Pairwise-complete covariance over sparse rows, with batch retraction.

    Parameters
    ----------
    n_variables:
        Explicit dimension.  Every row must have exactly this many entries.
    ddof:
        Delta degrees of freedom for the denominator.  ``1`` (default)
        gives the sample covariance ``comoment / (count - 1)``; ``0`` gives
        the population covariance ``comoment / count``.  A pair with
        ``count <= ddof`` is undefined (``None``).
    """

    def __init__(self, n_variables: int, *, ddof: int = 1) -> None:
        if not isinstance(n_variables, int) or n_variables <= 0:
            raise ValueError("n_variables must be a positive integer")
        if ddof < 0:
            raise ValueError("ddof must be >= 0")
        self.n = n_variables
        self.ddof = ddof
        self._pairs: dict[tuple[int, int], PairStats] = {}
        self._batches: dict[object, dict[tuple[int, int], PairStats]] = {}

    # -- validation ---------------------------------------------------------

    def _pair_key(self, i: int, j: int) -> tuple[int, int]:
        for idx in (i, j):
            if not isinstance(idx, int) or not 0 <= idx < self.n:
                raise IndexError(f"variable index {idx!r} out of range [0, {self.n})")
        return (i, j) if i <= j else (j, i)

    @staticmethod
    def _validate_value(value: object, row_no: int, col: int) -> Optional[float]:
        if value is None:
            return None
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise TypeError(
                f"row {row_no} col {col}: expected number or None, got {type(value).__name__}"
            )
        x = float(value)
        if not math.isfinite(x):
            raise ValueError(
                f"row {row_no} col {col}: non-finite value {x!r} is not allowed; "
                "use None for missing"
            )
        return x

    def _validate_rows(
        self, rows: Iterable[Sequence[Optional[float]]]
    ) -> list[list[Optional[float]]]:
        validated: list[list[Optional[float]]] = []
        for row_no, row in enumerate(rows):
            row = list(row)
            if len(row) != self.n:
                raise ValueError(
                    f"row {row_no} has {len(row)} entries; expected exactly {self.n}"
                )
            validated.append(
                [self._validate_value(v, row_no, c) for c, v in enumerate(row)]
            )
        return validated

    # -- ingestion / retraction ----------------------------------------------

    def add_batch(
        self, batch_id: object, rows: Iterable[Sequence[Optional[float]]]
    ) -> bool:
        """Ingest one batch.  Returns ``True`` if it was applied.

        Re-adding an existing ``batch_id`` is an idempotent no-op and
        returns ``False`` -- the data is never double-counted.
        """
        if batch_id in self._batches:
            return False
        validated = self._validate_rows(rows)

        evidence: dict[tuple[int, int], PairStats] = {}
        for i in range(self.n):
            for j in range(i, self.n):
                stats = PairStats()
                for row in validated:
                    x, y = row[i], row[j]
                    if x is not None and y is not None:
                        stats.update(x, y)
                if stats.count:
                    evidence[(i, j)] = stats

        for key, stats in evidence.items():
            current = self._pairs.get(key)
            self._pairs[key] = (
                stats.copy() if current is None else merge_stats(current, stats)
            )
        self._batches[batch_id] = evidence
        return True

    def remove_batch(self, batch_id: object) -> None:
        """Retract a previously added batch using its stored evidence.

        Raises ``KeyError`` for an unknown id (never silently ignored).
        """
        if batch_id not in self._batches:
            raise KeyError(f"unknown batch id: {batch_id!r}")
        evidence = self._batches.pop(batch_id)
        for key, stats in evidence.items():
            remaining = subtract_stats(self._pairs[key], stats)
            if remaining.count == 0:
                del self._pairs[key]
            else:
                self._pairs[key] = remaining

    # -- queries --------------------------------------------------------------

    def count(self, i: int, j: int) -> int:
        """Number of rows where both variables ``i`` and ``j`` are observed."""
        key = self._pair_key(i, j)
        stats = self._pairs.get(key)
        return stats.count if stats else 0

    def covariance(self, i: int, j: int, *, ddof: Optional[int] = None) -> Optional[float]:
        """Covariance of the pair, or ``None`` when undefined.

        Undefined iff ``count <= ddof`` (e.g. a single common observation
        cannot define a sample covariance with ddof=1).
        """
        key = self._pair_key(i, j)
        stats = self._pairs.get(key)
        d = self.ddof if ddof is None else ddof
        if d < 0:
            raise ValueError("ddof must be >= 0")
        if stats is None or stats.count <= d:
            return None
        return stats.comoment / (stats.count - d)

    def matrix(self, *, ddof: Optional[int] = None) -> list[list[Optional[float]]]:
        """Dense covariance matrix; undefined pairs are ``None`` holes."""
        result: list[list[Optional[float]]] = [
            [None] * self.n for _ in range(self.n)
        ]
        for i in range(self.n):
            for j in range(self.n):
                result[i][j] = self.covariance(i, j, ddof=ddof)
        return result

    def check_psd(
        self, *, ddof: Optional[int] = None, tol: float = 1e-9
    ) -> PSDDiagnostic:
        """Diagnose positive-semidefiniteness of the covariance matrix.

        Never alters the matrix.  Returns a counterexample vector when the
        matrix is not PSD, or lists the undefined pairs when it has holes.
        """
        undefined = [
            (i, j)
            for i in range(self.n)
            for j in range(i, self.n)
            if self.covariance(i, j, ddof=ddof) is None
        ]
        if undefined:
            return PSDDiagnostic(
                status="undefined_pairs",
                undefined_pairs=undefined,
                message=(
                    f"{len(undefined)} pair(s) undefined (count <= ddof); "
                    "PSD check requires a complete matrix"
                ),
            )
        dense = [[self.covariance(i, j, ddof=ddof) for j in range(self.n)] for i in range(self.n)]  # type: ignore[misc]
        eigenvalues, eigenvectors = _jacobi_eigendecomposition(dense)
        k = min(range(self.n), key=lambda idx: eigenvalues[idx])
        lam = eigenvalues[k]
        if lam < -tol:
            v = eigenvectors[k]
            return PSDDiagnostic(
                status="not_psd",
                min_eigenvalue=lam,
                counterexample=v,
                message=(
                    f"min eigenvalue {lam:.6g} < 0; v^T C v = "
                    f"{quadratic_form(dense, v):.6g} for the returned vector"
                ),
            )
        return PSDDiagnostic(
            status="psd",
            min_eigenvalue=lam,
            message=f"min eigenvalue {lam:.6g} >= -{tol:g}",
        )
