"""Brute-force active-set enumeration reference solver.

For small n this enumerates all 2^n zero/nonzero patterns, solves the
resulting equality-constrained QP exactly, keeps full KKT points, and
returns the best one. Used to cross-check the iterative solver and to give
an independent second opinion on small instances.
"""
from __future__ import annotations

from fractions import Fraction
from itertools import combinations
from typing import Optional, Sequence

from .linalg import row_basis, to_fractions
from .qp import (InfeasibleError, KKTCertificate, QPResult, _objective,
                 _solve_equality_qp, verify_kkt)
from .simplex import find_feasible


def solve_qp_enumeration(w: Sequence, y: Sequence,
                         A: Sequence[Sequence], b: Sequence,
                         max_n: int = 20) -> QPResult:
    """Exact projection by enumerating active sets. Only for small n."""
    w = to_fractions(w)
    y = to_fractions(y)
    b = to_fractions(b)
    a_full = [to_fractions(row) for row in A]
    n = len(w)
    if n > max_n:
        raise ValueError(f"enumeration reference limited to n <= {max_n}")
    try:
        basis_rows, a_red, b_red = row_basis(a_full, b)
    except ValueError as exc:
        raise InfeasibleError(str(exc)) from exc
    if a_red and find_feasible(a_red, b_red) is None:
        raise InfeasibleError("empty feasible set (enumeration reference)")

    best_x: Optional[list[Fraction]] = None
    best_obj: Optional[Fraction] = None
    best_lam_red: list[Fraction] = []
    indices = range(n)
    for size in range(n + 1):
        for zero_set in combinations(indices, size):
            free = [i for i in indices if i not in zero_set]
            solved = _solve_equality_qp(w, y, a_red, b_red, free)
            if solved is None:
                continue
            x_free, lam_red = solved
            if any(v < 0 for v in x_free):
                continue
            x = [Fraction(0)] * n
            for a, i in enumerate(free):
                x[i] = x_free[a]
            # full KKT check on the zero set
            mu_ok = True
            for i in zero_set:
                grad = 2 * w[i] * (x[i] - y[i])
                at_lam = sum(a_red[j][i] * lam_red[j]
                             for j in range(len(a_red)))
                if grad - at_lam < 0:
                    mu_ok = False
                    break
            if not mu_ok:
                continue
            obj = _objective(w, y, x)
            if best_obj is None or obj < best_obj:
                best_obj = obj
                best_x = x
                best_lam_red = lam_red
    if best_x is None:
        raise InfeasibleError("no KKT point found (enumeration reference)")

    lam = [Fraction(0)] * len(a_full)
    for pos, row_idx in enumerate(basis_rows):
        lam[row_idx] = best_lam_red[pos]
    mu = []
    for i in range(n):
        grad = 2 * w[i] * (best_x[i] - y[i])
        at_lam = sum(a_full[j][i] * lam[j] for j in range(len(a_full)))
        mu.append(grad - at_lam)
    cert = KKTCertificate(x=best_x, lam=lam, mu=mu)
    assert verify_kkt(w, y, a_full, b, cert)
    return QPResult(x=best_x, objective=best_obj, certificate=cert,
                    converged=True, iterations=-1, status="ok")
