"""Implicit-equation root solving and implicit-function-theorem derivatives.

``solve_implicit`` drives Newton's method on ``residual(x, y) = 0`` and
always reports whether it converged and whether the Jacobian looked
singular.  ``implicit_jvp`` differentiates the *converged* root via
``dy/dx = -J_y^{-1} F_x``; it raises instead of ever returning a
derivative computed from an unconverged iterate or a singular Jacobian.

Singularity detection is two-layered:

1. a direct (scale-relative) pivot test on ``J_y`` -- catches exact or
   near-exact zeros, e.g. starting exactly on the singular root;
2. a convergence-rate test -- at a simple root Newton converges
   quadratically, while a singular Jacobian forces linear convergence
   (for ``y^2 = x`` at ``x = 0`` the iterates halve each step).  A root
   reached only linearly is reported as ``jacobian_singular``.
"""
from __future__ import annotations

import math
from dataclasses import dataclass, field

from .ad import jvp
from .budget import budget_scope
from .errors import NonConvergenceError, SingularJacobianError
from .tensor import is_singular, solve

#: Step-ratio above which Newton is deemed merely linearly convergent.
_LINEAR_RATE_THRESHOLD = 0.35


@dataclass
class SolveResult:
    converged: bool
    y: list
    iterations: int
    residual_norm: float
    jacobian_singular: bool = False
    message: str = ""


def _identity(n):
    return [[1.0 if i == j else 0.0 for j in range(n)] for i in range(n)]


def _norm(v):
    return math.sqrt(sum(c * c for c in v))


def solve_implicit(residual, x, y0, *, tol=1e-12, max_iter=50, budget=None):
    """Newton-solve ``residual(x, y) = 0`` for ``y`` starting from ``y0``.

    Returns a :class:`SolveResult`.  A hard singularity of ``J_y`` during
    iteration raises :class:`SingularJacobianError` immediately.
    """
    x = [float(v) for v in x]
    y = [float(v) for v in y0]

    def run():
        return _newton(residual, x, list(y), tol, max_iter)

    if budget is not None:
        with budget_scope(budget):
            return run()
    return run()


def _newton(residual, x, y, tol, max_iter):
    m = len(y)
    steps = []
    rn = math.inf
    for it in range(max_iter + 1):
        F, cols = jvp(lambda yv: residual(x, yv), y, _identity(m))
        rn = _norm(F)
        J = [[cols[k][o] for k in range(m)] for o in range(len(F))]
        if rn <= tol:
            singular = is_singular(J)
            msg = ""
            if not singular and len(steps) >= 2 and steps[-1] > _LINEAR_RATE_THRESHOLD * steps[-2]:
                singular = True
                msg = ("converged only linearly -> Jacobian singular at the "
                       "root; implicit function theorem does not apply")
            return SolveResult(True, list(y), it, rn, singular, msg)
        if it == max_iter:
            break
        delta = solve(J, [-f for f in F])  # raises SingularJacobianError
        steps.append(_norm(delta))
        y = [yi + di for yi, di in zip(y, delta)]
    return SolveResult(False, list(y), max_iter, rn, False,
                       f"Newton did not converge in {max_iter} iterations "
                       f"(residual {rn:.3e})")


def implicit_jvp(residual, x, y0, directions, *, tol=1e-12, max_iter=50,
                 budget=None):
    """Differentiate the implicit root along directions in ``x``.

    ``directions[k]`` is a direction in parameter space; returns
    ``(dydx, result)`` where ``dydx[k]`` is the pushed-forward root
    direction.  Raises :class:`NonConvergenceError` when the solve did not
    converge and :class:`SingularJacobianError` when ``J_y`` is singular at
    the root -- never a fabricated derivative.
    """
    result = solve_implicit(residual, x, y0, tol=tol, max_iter=max_iter,
                            budget=budget)
    if not result.converged:
        raise NonConvergenceError(
            "refusing to differentiate an unconverged solve: " + result.message)
    if result.jacobian_singular:
        raise SingularJacobianError(
            "Jacobian singular at the root; implicit function theorem does "
            "not apply. " + result.message)
    x = [float(v) for v in x]
    y = result.y
    m = len(y)
    _, cols_y = jvp(lambda yv: residual(x, yv), y, _identity(m))
    Jy = [[cols_y[k][o] for k in range(m)] for o in range(m)]
    _, cols_x = jvp(lambda xv: residual(xv, y), x, [list(d) for d in directions])
    dydx = [solve(Jy, [-c for c in col]) for col in cols_x]
    return dydx, result
