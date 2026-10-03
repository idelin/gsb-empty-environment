"""Differentiable implicit root finding.

Scalar case: solve F(x, y) = 0 for y by Newton iteration, then apply the
implicit function theorem dy/dx = -F_x / F_y at the converged root.

System case: solve F(x, y) = 0 with y, F in R^n by Newton iteration using
the multi-directional JVP as the Jacobian and a hand-rolled dense solve,
then dy/dx = -J_y^{-1} F_x.

Both report convergence and jacobian singularity explicitly, and refuse
to hand out a derivative for a non-converged or singular solve.
"""
from __future__ import annotations

import math
from dataclasses import dataclass

from . import budget as _budget
from . import tensor
from .diff import derivative, jacobian
from .dual import deep_primal
from .errors import (NotConvergedError, SingularJacobianError,
                     SingularMatrixError)

__all__ = ["RootResult", "solve_scalar", "solve_system",
           "implicit_derivative", "implicit_jacobian"]


@dataclass
class RootResult:
    root: object
    converged: bool
    iterations: int
    residual: float
    jacobian: object
    singular: bool
    message: str = ""


def _eff_sing_tol(sing_tol, tol, scale):
    if sing_tol is not None:
        return sing_tol
    # sqrt(tol) scaling: at a multiple root Newton converges only linearly
    # and |F_y| at the "converged" iterate is O(sqrt(tol)), so this catches
    # roots where the jacobian vanishes, without flagging healthy ones.
    return max(1e-12, math.sqrt(tol)) * (1.0 + abs(scale))


def solve_scalar(F, x, y0, *, tol=1e-12, max_iter=50, sing_tol=None):
    """Newton solve of F(x, y) = 0 for y. Never raises for non-convergence;
    inspect the returned RootResult instead."""
    x = deep_primal(x)
    y = float(y0)
    fy = float("nan")
    eff_sing = None
    for it in range(max_iter + 1):
        _budget.tick()
        r = deep_primal(F(x, y))
        fy = deep_primal(derivative(lambda yy: F(x, yy), y))
        if eff_sing is None:
            eff_sing = _eff_sing_tol(sing_tol, tol, fy)
        singular = abs(fy) <= eff_sing
        if abs(r) <= tol:
            return RootResult(
                root=y, converged=True, iterations=it, residual=abs(r),
                jacobian=fy, singular=singular,
                message="converged" + (" (jacobian singular at root)" if singular else ""))
        if singular:
            return RootResult(y, False, it, abs(r), fy, True,
                              "singular jacobian before convergence")
        if it == max_iter:
            break
        y -= r / fy
    r = deep_primal(F(x, y))
    return RootResult(y, False, max_iter, abs(r), fy, abs(fy) <= eff_sing,
                      "failed to converge within max_iter")


def implicit_derivative(F, x, y0, **kw):
    """dy/dx at x for the branch of F(x, y) = 0 reached from y0.

    Raises NotConvergedError or SingularJacobianError instead of returning
    a derivative computed from an untrustworthy iterate.
    """
    result = solve_scalar(F, x, y0, **kw)
    if result.singular:
        raise SingularJacobianError(result)
    if not result.converged:
        raise NotConvergedError(result)
    y = result.root
    fx = derivative(lambda xx: F(xx, y), x)
    fy = derivative(lambda yy: F(x, yy), y)
    return deep_primal(-fx / fy)


def solve_system(F, x, y0, *, tol=1e-12, max_iter=50, sing_tol=None):
    """Newton solve of F(x, y) = 0 with y and F vector-valued.

    x may be a scalar or a list of parameters. The Jacobian is assembled
    with one multi-directional JVP per iteration.
    """
    y = [float(v) for v in y0]
    n = len(y)
    J = None
    eff_sing = None
    for it in range(max_iter + 1):
        _budget.tick()
        primals, J = jacobian(lambda yy: F(x, yy), y)
        r = [deep_primal(v) for v in primals]
        J = [[deep_primal(v) for v in row] for row in J]
        if eff_sing is None:
            # tensor.solve row-equilibrates, so an absolute tolerance is
            # meaningful; sqrt(tol) catches vanishing-jacobian roots.
            eff_sing = sing_tol if sing_tol is not None else max(1e-12, math.sqrt(tol))
        residual = tensor.norm_inf(r)
        singular = _is_singular(J, eff_sing)
        if residual <= tol:
            return RootResult(
                root=list(y), converged=True, iterations=it, residual=residual,
                jacobian=J, singular=singular,
                message="converged" + (" (jacobian singular at root)" if singular else ""))
        if singular:
            return RootResult(list(y), False, it, residual, J, True,
                              "singular jacobian before convergence")
        if it == max_iter:
            break
        step = tensor.solve(J, [-v for v in r], tol=eff_sing)
        y = [yi + di for yi, di in zip(y, step)]
    r = [deep_primal(v) for v in F(x, list(y))]
    return RootResult(list(y), False, max_iter, tensor.norm_inf(r), J,
                      _is_singular(J, eff_sing),
                      "failed to converge within max_iter")


def _is_singular(J, tol):
    try:
        tensor.solve(J, [0.0] * len(J), tol=tol)
        return False
    except SingularMatrixError:
        return True


def implicit_jacobian(F, xs, y0, **kw):
    """n x m matrix dy/dx at xs for the branch of F(x, y) = 0 from y0."""
    xs = [deep_primal(v) for v in xs]
    result = solve_system(F, xs, list(y0), **kw)
    if result.singular:
        raise SingularJacobianError(result)
    if not result.converged:
        raise NotConvergedError(result)
    y = result.root
    Jy = result.jacobian
    _, Fx = jacobian(lambda xx: F(xx, y), xs)
    Fx = [[deep_primal(v) for v in row] for row in Fx]
    n = len(y)
    m = len(xs)
    sing = kw.get("sing_tol") or 1e-12
    columns = []
    for j in range(m):
        rhs = [-Fx[i][j] for i in range(n)]
        try:
            columns.append(tensor.solve(Jy, rhs, tol=sing))
        except SingularMatrixError as exc:
            raise SingularJacobianError(result) from exc
    return [[columns[j][i] for j in range(m)] for i in range(n)]
