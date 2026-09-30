"""Exact two-phase simplex feasibility check for {x >= 0 : A x = b}.

Uses Bland's rule, which guarantees termination (no cycling) with exact
Fraction arithmetic. Only Phase 1 is needed here: it either produces a
feasible point or proves the feasible set is empty.
"""
from __future__ import annotations

from fractions import Fraction
from typing import Optional, Sequence


def find_feasible(A: Sequence[Sequence[Fraction]],
                  b: Sequence[Fraction]) -> Optional[list[Fraction]]:
    """Return an exact feasible point x >= 0 with A x = b, or None if the
    feasible set is empty."""
    m = len(A)
    n = len(A[0]) if m else 0
    if m == 0:
        return [Fraction(0)] * n
    # Normalize rows so that b >= 0.
    rows = []
    rhs = []
    for j in range(m):
        row = [Fraction(v) for v in A[j]]
        bj = Fraction(b[j])
        if bj < 0:
            row = [-v for v in row]
            bj = -bj
        rows.append(row)
        rhs.append(bj)

    total = n + m  # x variables + artificials
    # Tableau: m rows x (total vars + 1 RHS). Artificial columns start as I.
    tab = [rows[i] + [Fraction(1) if k == i else Fraction(0)
                      for k in range(m)] + [rhs[i]] for i in range(m)]
    basis = [n + i for i in range(m)]          # artificials are basic
    cost = [Fraction(0)] * n + [Fraction(1)] * m

    def reduced_costs():
        rc = []
        for j in range(total):
            cj = cost[j] - sum(cost[basis[i]] * tab[i][j]
                               for i in range(m))
            rc.append(cj)
        return rc

    def pivot(r, c):
        pv = tab[r][c]
        tab[r] = [v / pv for v in tab[r]]
        for i in range(m):
            if i != r and tab[i][c] != 0:
                factor = tab[i][c]
                tab[i] = [a - factor * bb for a, bb in
                          zip(tab[i], tab[r])]
        basis[r] = c

    # ---- Phase 1: minimize sum of artificials (Bland's rule) ------------
    while True:
        rc = reduced_costs()
        entering = next((j for j in range(total) if rc[j] < 0), None)
        if entering is None:
            break
        best_ratio = None
        leaving = None
        for i in range(m):
            if tab[i][entering] > 0:
                ratio = tab[i][total] / tab[i][entering]
                if (best_ratio is None or ratio < best_ratio or
                        (ratio == best_ratio and basis[i] < basis[leaving])):
                    best_ratio = ratio
                    leaving = i
        if leaving is None:
            raise ArithmeticError("phase-1 problem unbounded (impossible)")
        pivot(leaving, entering)

    obj = sum(tab[i][total] for i in range(m) if basis[i] >= n)
    if obj != 0:
        return None  # infeasible

    # Drive any remaining basic artificials (value 0) out of the basis.
    for i in range(m):
        if basis[i] >= n:
            pivot_col = next((j for j in range(n) if tab[i][j] != 0), None)
            if pivot_col is not None:
                pivot(i, pivot_col)
            # else: redundant row; artificial stays basic at value 0.

    x = [Fraction(0)] * n
    for i in range(m):
        if basis[i] < n:
            x[basis[i]] = tab[i][total]
    return x
