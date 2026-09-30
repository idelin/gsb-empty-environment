"""Exact rational QP projection with non-negativity and linear equality
constraints, solved by a primal active-set method over Fraction arithmetic.

Problem form
------------
    minimize    f(x) = sum_i w_i * (x_i - y_i)^2
    subject to  A x = b
                x >= 0

with strictly positive rational weights w_i > 0. The objective is strictly
convex, so the minimizer is unique and is fully characterized by the KKT
conditions:

    stationarity          2 w_i (x_i - y_i) - (A^T lam)_i - mu_i = 0
    primal feasibility    A x = b,  x >= 0
    dual feasibility      mu >= 0
    complementary slack.  mu_i * x_i = 0

The solver returns a KKTCertificate (x, lam, mu) that any independent party
can verify exactly with `verify_kkt` -- no trust in the solver required.

Infeasibility of the equality/box system (e.g. a negative total target) is
detected by an exact Phase-1 QP and reported via InfeasibleError. If the
iteration budget is exhausted the result carries converged=False.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import Optional, Sequence

from .linalg import row_basis, solve_linear, to_fractions
from .simplex import find_feasible


class InfeasibleError(Exception):
    """Raised when {x >= 0 : A x = b} is empty (e.g. negative total target)."""


@dataclass
class KKTCertificate:
    """Independently checkable KKT evidence for the projection QP."""
    x: list[Fraction]
    lam: list[Fraction]   # one entry per row of the *original* A
    mu: list[Fraction]


@dataclass
class QPResult:
    x: list[Fraction]
    objective: Fraction
    certificate: Optional[KKTCertificate]
    converged: bool
    iterations: int
    status: str           # "ok" | "not_converged"


def verify_kkt(w: Sequence[Fraction], y: Sequence[Fraction],
               A: Sequence[Sequence[Fraction]], b: Sequence[Fraction],
               cert: KKTCertificate) -> bool:
    """Independently verify a KKT certificate with exact arithmetic."""
    n = len(w)
    m = len(A)
    x, lam, mu = cert.x, cert.lam, cert.mu
    if not (len(x) == len(y) == len(mu) == n and len(lam) == m):
        return False
    if any(v <= 0 for v in w):
        return False
    # primal feasibility
    if any(xi < 0 for xi in x):
        return False
    for j in range(m):
        if sum(Fraction(A[j][i]) * x[i] for i in range(n)) != Fraction(b[j]):
            return False
    # dual feasibility
    if any(mui < 0 for mui in mu):
        return False
    # stationarity + complementary slackness
    for i in range(n):
        grad = 2 * Fraction(w[i]) * (x[i] - Fraction(y[i]))
        at_lam = sum(Fraction(A[j][i]) * lam[j] for j in range(m))
        if grad - at_lam != mu[i]:
            return False
        if mu[i] * x[i] != 0:
            return False
    return True


def _objective(w, y, x) -> Fraction:
    return sum(Fraction(w[i]) * (x[i] - Fraction(y[i])) ** 2
               for i in range(len(w)))


def _solve_equality_qp(w, y, a_red, b_red, free):
    """Minimize sum w_i (x_i - y_i)^2 over `free` indices s.t. A_red x = b
    (variables outside `free` are fixed to 0). Returns (x_free, lam_red) or
    None if the reduced system is inconsistent."""
    k = len(free)
    m = len(a_red)
    size = k + m
    mat = [[Fraction(0)] * size for _ in range(size)]
    rhs = [Fraction(0)] * size
    for a in range(k):
        i = free[a]
        mat[a][a] = 2 * Fraction(w[i])
        rhs[a] = 2 * Fraction(w[i]) * Fraction(y[i])
        for j in range(m):
            mat[a][k + j] = Fraction(a_red[j][i])
    for j in range(m):
        for a in range(k):
            mat[k + j][a] = Fraction(a_red[j][free[a]])
        rhs[k + j] = Fraction(b_red[j])
    sol = solve_linear(mat, rhs)
    if sol is None:
        return None
    # The block system assembles grad + A^T s = 0 with s = -lam; flip the
    # sign so the returned multipliers satisfy grad = A^T lam (KKT form).
    return sol[:k], [-v for v in sol[k:]]


def _active_set(w, y, a_red, b_red, x0, max_iterations):
    """Primal active-set loop from a feasible point x0.

    Returns (x, lam_red, converged, iterations)."""
    n = len(w)
    m = len(a_red)
    x = list(x0)
    active = {i for i in range(n) if x[i] == 0}
    lam_red = [Fraction(0)] * m
    iterations = 0
    for iterations in range(1, max_iterations + 1):
        free = [i for i in range(n) if i not in active]
        solved = _solve_equality_qp(w, y, a_red, b_red, free)
        if solved is None:
            # Working set became inconsistent; should not happen from a
            # feasible start, but fail honestly if it does.
            return x, lam_red, False, iterations
        x_free, lam_red = solved
        if all(v >= 0 for v in x_free):
            for a, i in enumerate(free):
                x[i] = x_free[a]
            # KKT multipliers on the active (zero) set
            mu = {}
            for i in active:
                grad = 2 * Fraction(w[i]) * (x[i] - Fraction(y[i]))
                at_lam = sum(Fraction(a_red[j][i]) * lam_red[j]
                             for j in range(m))
                mu[i] = grad - at_lam
            violators = [i for i in active if mu[i] < 0]
            if not violators:
                return x, lam_red, True, iterations
            # Bland-style rule: drop the smallest-index violator.
            active.remove(min(violators))
        else:
            # Line search from current x toward the unconstrained (on the
            # free set) minimizer; block at the first bound hit.
            alpha = Fraction(1)
            blockers = []
            for a, i in enumerate(free):
                d = x_free[a] - x[i]
                if d < 0:
                    ratio = x[i] / (-d)
                    if ratio < alpha:
                        alpha = ratio
                        blockers = [i]
                    elif ratio == alpha:
                        blockers.append(i)
            for a, i in enumerate(free):
                x[i] = x[i] + alpha * (x_free[a] - x[i])
            if alpha < 1:
                for i in blockers:
                    x[i] = Fraction(0)
                    active.add(i)
    return x, lam_red, False, max_iterations


def solve_qp(w: Sequence, y: Sequence,
             A: Sequence[Sequence], b: Sequence,
             max_iterations: int = 1000) -> QPResult:
    """Project y onto {x >= 0 : A x = b} under the weighted squared norm.

    All inputs are converted to exact Fractions. Raises InfeasibleError if
    the feasible set is empty. If the iteration budget is exhausted, the
    returned QPResult has converged=False and status="not_converged".
    """
    w = to_fractions(w)
    y = to_fractions(y)
    b = to_fractions(b)
    a_full = [to_fractions(row) for row in A]
    n = len(w)
    if any(v <= 0 for v in w):
        raise ValueError("weights must be strictly positive")
    if len(y) != n or any(len(row) != n for row in a_full):
        raise ValueError("dimension mismatch")
    if len(a_full) != len(b):
        raise ValueError("A/b row mismatch")

    # Reduce equalities to an independent row set (exact).
    try:
        basis_rows, a_red, b_red = row_basis(a_full, b)
    except ValueError as exc:
        raise InfeasibleError(str(exc)) from exc

    # ---- Phase 1: exact feasibility via two-phase simplex --------------
    if not a_red:
        x_start = [Fraction(0)] * n
    else:
        x_start = find_feasible(a_red, b_red)
    if x_start is None:
        raise InfeasibleError(
            "empty feasible set: no x >= 0 satisfies A x = b "
            "(e.g. negative total target)")

    # ---- Phase 2: the actual projection ---------------------------------
    x, lam_red, converged, iterations = _active_set(
        w, y, a_red, b_red, x_start, max_iterations)
    if not converged:
        return QPResult(x=x, objective=_objective(w, y, x),
                        certificate=None, converged=False,
                        iterations=iterations, status="not_converged")

    # Map reduced-row multipliers back to the original rows (lambda = 0 on
    # dependent rows; this is still a valid KKT pair).
    lam = [Fraction(0)] * len(a_full)
    for pos, row_idx in enumerate(basis_rows):
        lam[row_idx] = lam_red[pos]
    mu = []
    for i in range(n):
        grad = 2 * w[i] * (x[i] - y[i])
        at_lam = sum(a_full[j][i] * lam[j] for j in range(len(a_full)))
        mu.append(grad - at_lam)
    cert = KKTCertificate(x=list(x), lam=lam, mu=mu)
    if not verify_kkt(w, y, a_full, b, cert):
        # Exact self-check failed: refuse to emit an unchecked answer.
        return QPResult(x=x, objective=_objective(w, y, x),
                        certificate=None, converged=False,
                        iterations=iterations, status="not_converged")
    return QPResult(x=list(x), objective=_objective(w, y, x),
                    certificate=cert, converged=True,
                    iterations=iterations, status="ok")
