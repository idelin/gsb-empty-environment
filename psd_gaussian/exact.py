"""Exact rational-algebra verification for small matrices.

Uses :class:`fractions.Fraction` so every conclusion here is *exact*, in
contrast to the floating-point spectral code (whose residuals are exact
deterministic facts about the float factorization) and to empirical sample
covariances (which are statistical estimates only).

The exact decomposition verified here is the rational ``L D L^T``
factorization: a rational symmetric matrix is PSD iff an exact unit lower
triangular ``L`` and diagonal ``D >= 0`` exist with ``Sigma = L D L^T``.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

FMatrix = list[list[Fraction]]


def to_fraction_matrix(rows) -> FMatrix:
    matrix = [[Fraction(x) for x in row] for row in rows]
    if not matrix or not matrix[0]:
        raise ValueError("matrix must be non-empty")
    width = len(matrix[0])
    if any(len(row) != width for row in matrix):
        raise ValueError("matrix is not rectangular")
    return matrix


def quadratic_form_exact(v: list[Fraction], m: FMatrix) -> Fraction:
    return sum(v[i] * m[i][j] * v[j] for i in range(len(v)) for j in range(len(v)))


@dataclass
class ExactPSDReport:
    """Exact PSD verdict for a rational symmetric matrix.

    ``is_psd`` is an exact conclusion.  When True, ``L``/``D`` give the exact
    factorization ``Sigma = L D L^T``.  When False, ``witness`` is a rational
    vector with ``witness^T Sigma witness == witness_value < 0`` exactly.
    """

    is_psd: bool
    L: FMatrix | None
    D: list[Fraction] | None
    witness: list[Fraction] | None
    witness_value: Fraction | None
    kind: str = "exact"


def exact_check_psd(cov) -> ExactPSDReport:
    """Exact rational LDL^T-based PSD test (no floating point involved)."""
    m = to_fraction_matrix(cov)
    n = len(m)
    if any(len(row) != n for row in m):
        raise ValueError("matrix must be square")
    for i in range(n):
        for j in range(i + 1, n):
            if m[i][j] != m[j][i]:
                raise ValueError("matrix is not exactly symmetric")

    a = [row[:] for row in m]
    L = [[Fraction(int(i == j)) for j in range(n)] for i in range(n)]
    D = [Fraction(0) for _ in range(n)]

    for i in range(n):
        pivot = a[i][i]
        if pivot == 0:
            # In a PSD matrix a zero diagonal forces the whole row/column to 0.
            for j in range(n):
                if a[i][j] != 0 or a[j][i] != 0:
                    off = a[i][j] if a[i][j] != 0 else a[j][i]
                    diag = a[j][j]
                    t = -(diag + 1) / (2 * off)
                    witness = [Fraction(0) for _ in range(n)]
                    witness[i] = t
                    witness[j] = Fraction(1)
                    return ExactPSDReport(
                        False, None, None, witness,
                        quadratic_form_exact(witness, m),
                    )
            continue
        D[i] = pivot
        for j in range(i + 1, n):
            L[j][i] = a[j][i] / pivot
        for j in range(i + 1, n):
            for k in range(i + 1, n):
                a[j][k] -= L[j][i] * pivot * L[k][i]
        for j in range(i + 1, n):
            a[j][i] = Fraction(0)
            a[i][j] = Fraction(0)

    for i, di in enumerate(D):
        if di < 0:
            # Sigma = L D L^T exactly, so solving L^T v = e_i gives
            # v^T Sigma v = D_i < 0: an exact witness.
            v = [Fraction(0) for _ in range(n)]
            for j in range(n - 1, -1, -1):
                v[j] = (Fraction(int(j == i))
                        - sum(L[k][j] * v[k] for k in range(j + 1, n)))
            return ExactPSDReport(False, L, D, v, quadratic_form_exact(v, m))
    return ExactPSDReport(True, L, D, None, None)


def reconstruct_exact(L: FMatrix, D: list[Fraction]) -> FMatrix:
    n = len(L)
    return [
        [sum(L[i][k] * D[k] * L[j][k] for k in range(n)) for j in range(n)]
        for i in range(n)
    ]


def exact_verify_decomposition(L: FMatrix, D: list[Fraction], cov) -> bool:
    """Exact check of the decomposition relation ``L D L^T == Sigma``."""
    m = to_fraction_matrix(cov)
    return reconstruct_exact(L, D) == m
