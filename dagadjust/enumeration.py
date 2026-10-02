"""Brute-force active-set enumeration over exact rationals.

For small instances this enumerates every candidate active set of the
bound constraints x >= 0, solves the corresponding equality QP, keeps
the points satisfying all KKT conditions, and returns the best one.
Used to cross-check the iterative solver: the solver's termination is
justified by certificates, and this enumeration independently confirms
global optimality on small synthetic problems.
"""

from __future__ import annotations

from fractions import Fraction
from itertools import combinations

from .linalg import (
    SingularMatrixError,
    find_feasible_point,
    independent_rows,
    is_consistent,
)
from .solver import solve_eqp


def enumerate_active_sets(problem, max_vars=16):
    """Return (objective, x, active_set) of the best exact KKT point, or
    None if the problem is infeasible.  Raises ValueError for instances
    too large to enumerate."""
    n = problem.dag.n_nodes
    if n > max_vars:
        raise ValueError(f"instance too large for enumeration (n={n})")
    rows, rhs, _ = problem.constraints()
    if not is_consistent(rows, rhs):
        return None
    if problem.nonneg and find_feasible_point(rows, rhs) is None:
        return None
    reduced_rows, kept, _ = independent_rows(rows)
    reduced_rhs = [rhs[i] for i in kept]
    w = list(problem.weights)
    y = list(problem.observed)

    if problem.nonneg:
        subsets = [
            combo for r in range(n + 1) for combo in combinations(range(n), r)
        ]
    else:
        subsets = [()]

    best = None
    for subset in subsets:
        E = [list(row) for row in reduced_rows]
        f = list(reduced_rhs)
        for j in subset:
            row = [Fraction(0)] * n
            row[j] = Fraction(1)
            E.append(row)
            f.append(Fraction(0))
        try:
            x, lam_all = solve_eqp(w, y, E, f)
        except SingularMatrixError:
            continue
        if problem.nonneg:
            if any(xi < 0 for xi in x):
                continue
            nu = lam_all[len(reduced_rows):]
            if any(-nu_j < 0 for nu_j in nu):
                continue
        objective = sum(w[i] * (x[i] - y[i]) ** 2 for i in range(n)) / 2
        if best is None or objective < best[0]:
            best = (objective, x, sorted(subset))
    return best
