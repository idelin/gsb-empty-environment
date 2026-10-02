"""Exact rational linear algebra built on fractions.Fraction.

Everything here is exact: no floating point, no tolerances. Singularity,
rank deficiency and inconsistency are detected structurally, not by
comparing small numbers against an epsilon.
"""

from __future__ import annotations

from fractions import Fraction


class SingularMatrixError(Exception):
    """Raised when a square system has no unique exact solution."""

    def __init__(self, free_columns):
        self.free_columns = list(free_columns)
        super().__init__(f"singular matrix; free columns: {self.free_columns}")


def rref(matrix, ncols=None):
    """Reduced row echelon form. Returns (reduced_rows, pivot_columns).

    ``ncols`` limits pivoting to the first ``ncols`` columns (any extra
    columns are still eliminated against the pivots).
    """
    a = [list(row) for row in matrix]
    n_rows = len(a)
    if n_rows == 0:
        return a, []
    n = ncols if ncols is not None else len(a[0])
    pivots = []
    r = 0
    for c in range(n):
        pivot = next((i for i in range(r, n_rows) if a[i][c] != 0), None)
        if pivot is None:
            continue
        a[r], a[pivot] = a[pivot], a[r]
        inv = Fraction(1) / a[r][c]
        a[r] = [v * inv for v in a[r]]
        for i in range(n_rows):
            if i != r and a[i][c] != 0:
                factor = a[i][c]
                a[i] = [v - factor * w for v, w in zip(a[i], a[r])]
        pivots.append(c)
        r += 1
        if r == n_rows:
            break
    return a, pivots


def rank(matrix):
    if not matrix:
        return 0
    _, pivots = rref(matrix)
    return len(pivots)


def independent_rows(rows):
    """Split rows into an independent basis and redundant rows.

    Returns (kept_rows, kept_indices, redundant_indices). Redundant rows
    are exact linear combinations of the kept ones.
    """
    kept = []
    kept_indices = []
    redundant_indices = []
    current = 0
    for i, row in enumerate(rows):
        trial = rank(kept + [row])
        if trial > current:
            kept.append(list(row))
            kept_indices.append(i)
            current = trial
        else:
            redundant_indices.append(i)
    return kept, kept_indices, redundant_indices


def is_consistent(rows, rhs):
    """True iff the linear system rows @ x = rhs has a solution."""
    if not rows:
        return True
    augmented = [list(row) + [rhs[i]] for i, row in enumerate(rows)]
    return rank(rows) == rank(augmented)


def solve_square(matrix, rhs):
    """Solve a square system exactly. Raises SingularMatrixError if not
    uniquely solvable, reporting the free (non-pivot) column indices."""
    n = len(matrix)
    if n == 0:
        return []
    augmented = [list(matrix[i]) + [rhs[i]] for i in range(n)]
    reduced, pivots = rref(augmented, ncols=n)
    if len(pivots) < n:
        pivot_set = set(pivots)
        free = [c for c in range(n) if c not in pivot_set]
        raise SingularMatrixError(free)
    x = [Fraction(0)] * n
    for r, c in enumerate(pivots):
        x[c] = reduced[r][n]
    return x


def find_feasible_point(rows, rhs):
    """Find x >= 0 with rows @ x = rhs, or return None if infeasible.

    Exact phase-I simplex with Bland's rule (guaranteed to terminate).
    Used to certify (in)feasibility of fixed counts under nonnegativity.
    """
    m = len(rows)
    if m == 0:
        return []
    n = len(rows[0])
    a = []
    b = []
    for row, value in zip(rows, rhs):
        if value < 0:
            a.append([-v for v in row])
            b.append(-value)
        else:
            a.append(list(row))
            b.append(value)
    total = n + m
    tableau = []
    for i in range(m):
        artificial = [Fraction(1) if j == i else Fraction(0) for j in range(m)]
        tableau.append(a[i] + artificial + [b[i]])
    basis = [n + i for i in range(m)]
    cost = [Fraction(0)] * n + [Fraction(1)] * m

    def reduced_costs():
        cbar = list(cost)
        for i in range(m):
            cb = cost[basis[i]]
            if cb:
                for j in range(total):
                    if tableau[i][j]:
                        cbar[j] -= cb * tableau[i][j]
        return cbar

    def objective():
        return sum(cost[basis[i]] * tableau[i][total] for i in range(m))

    for _ in range(100000):
        cbar = reduced_costs()
        entering = next((j for j in range(total) if cbar[j] < 0), None)
        if entering is None:
            break
        best_key = None
        leaving = None
        for i in range(m):
            if tableau[i][entering] > 0:
                key = (tableau[i][total] / tableau[i][entering], basis[i])
                if best_key is None or key < best_key:
                    best_key = key
                    leaving = i
        if leaving is None:
            raise ArithmeticError("phase-I problem unbounded (unexpected)")
        pv = tableau[leaving][entering]
        tableau[leaving] = [v / pv for v in tableau[leaving]]
        for i in range(m):
            if i != leaving and tableau[i][entering] != 0:
                factor = tableau[i][entering]
                tableau[i] = [
                    v - factor * w for v, w in zip(tableau[i], tableau[leaving])
                ]
        basis[leaving] = entering
    if objective() > 0:
        return None
    x = [Fraction(0)] * n
    for i in range(m):
        if basis[i] < n:
            x[basis[i]] = tableau[i][total]
    return x
