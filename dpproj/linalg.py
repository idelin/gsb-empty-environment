"""Exact rational linear algebra helpers (Fraction-based).

All computations are exact so that KKT certificates can be verified
independently with zero numerical tolerance.
"""
from __future__ import annotations

from fractions import Fraction
from typing import Optional, Sequence


def to_fractions(vec: Sequence) -> list[Fraction]:
    return [v if isinstance(v, Fraction) else Fraction(v) for v in vec]


def solve_linear(matrix: Sequence[Sequence[Fraction]],
                 rhs: Sequence[Fraction]) -> Optional[list[Fraction]]:
    """Solve M x = rhs exactly over the rationals.

    Returns one particular solution (free variables set to 0), or None if
    the system is inconsistent. Works for singular-but-consistent systems.
    """
    n_rows = len(matrix)
    n_cols = len(matrix[0]) if n_rows else 0
    aug = [[Fraction(v) for v in row] + [Fraction(rhs[i])]
           for i, row in enumerate(matrix)]
    pivot_cols: list[int] = []
    r = 0
    for c in range(n_cols):
        piv = next((i for i in range(r, n_rows) if aug[i][c] != 0), None)
        if piv is None:
            continue
        aug[r], aug[piv] = aug[piv], aug[r]
        pv = aug[r][c]
        aug[r] = [v / pv for v in aug[r]]
        for i in range(n_rows):
            if i != r and aug[i][c] != 0:
                factor = aug[i][c]
                aug[i] = [a - factor * b for a, b in zip(aug[i], aug[r])]
        pivot_cols.append(c)
        r += 1
        if r == n_rows:
            break
    for i in range(r, n_rows):
        if aug[i][n_cols] != 0:
            return None
    x = [Fraction(0)] * n_cols
    for i, c in enumerate(pivot_cols):
        x[c] = aug[i][n_cols]
    return x


def rank(matrix: Sequence[Sequence[Fraction]]) -> int:
    """Exact row rank over the rationals."""
    if not matrix:
        return 0
    n_rows = len(matrix)
    n_cols = len(matrix[0])
    aug = [[Fraction(v) for v in row] for row in matrix]
    r = 0
    for c in range(n_cols):
        piv = next((i for i in range(r, n_rows) if aug[i][c] != 0), None)
        if piv is None:
            continue
        aug[r], aug[piv] = aug[piv], aug[r]
        pv = aug[r][c]
        aug[r] = [v / pv for v in aug[r]]
        for i in range(n_rows):
            if i != r and aug[i][c] != 0:
                factor = aug[i][c]
                aug[i] = [a - factor * b for a, b in zip(aug[i], aug[r])]
        r += 1
        if r == n_rows:
            break
    return r


def row_basis(matrix: Sequence[Sequence[Fraction]],
              rhs: Sequence[Fraction]):
    """Select a subset of rows forming a basis of the row space of A.

    Returns (basis_rows, A_red, b_red) where basis_rows are indices into the
    original rows, so A_red x = b_red has the same solution set as A x = b.
    Raises ValueError if the system A x = b is inconsistent.
    """
    n_rows = len(matrix)
    if n_rows and solve_linear(matrix, rhs) is None:
        raise ValueError("inconsistent linear system: no x with A x = b")
    basis_rows: list[int] = []
    current: list[list[Fraction]] = []
    for i in range(n_rows):
        candidate = current + [[Fraction(v) for v in matrix[i]]]
        if rank(candidate) > rank(current):
            basis_rows.append(i)
            current = candidate
    a_red = [[Fraction(v) for v in matrix[i]] for i in basis_rows]
    b_red = [Fraction(rhs[i]) for i in basis_rows]
    return basis_rows, a_red, b_red
