"""Higher-level differentiation APIs built on tagged duals.

Every public entry point allocates fresh tags, so nested calls (an inner
``derivative`` whose body references variables perturbed by an outer
``derivative``) are isolated by construction.  Tangent components returned
by these functions are *not* force-unwrapped: when called inside another
differentiation they may themselves be Duals of the outer tag, which is
exactly the information the outer computation needs.  Use
:func:`dualtag.dual.primal_value` to fully unwrap at the top level.
"""
from __future__ import annotations

from .budget import budget_scope
from .dual import Dual, fresh_tag, is_dual, primal_value


def _run(budget, fn):
    if budget is None:
        return fn()
    with budget_scope(budget):
        return fn()


def _comp(v, k, tag):
    """Tangent component ``k`` of ``v`` if ``v`` belongs to ``tag``."""
    if is_dual(v) and v.tag.ident == tag.ident:
        return v.tangent[k]
    return 0.0


def _identity(n):
    return [[1.0 if i == j else 0.0 for j in range(n)] for i in range(n)]


def derivative(f, x0, budget=None):
    """First derivative of a univariate ``f`` at ``x0``.

    Returns ``(value, derivative)``.  ``x0`` may itself be a Dual of an
    older tag (nested differentiation); the returned derivative then keeps
    the outer perturbation instead of confusing it with the inner one.
    """
    tag = fresh_tag(1)

    def run():
        out = f(Dual(tag, x0, [1.0]))
        return primal_value(out), _comp(out, 0, tag)

    return _run(budget, run)


def second_derivative(f, x0, budget=None):
    """Univariate second derivative via two nested tags.

    Seeds ``x = x0 + e1 + e2`` with independent infinitesimals; the
    coefficient of ``e1*e2`` is ``f''``.  Returns ``(value, first, second)``.
    """
    t1 = fresh_tag(1)
    t2 = fresh_tag(1)

    def run():
        out = f(Dual(t2, Dual(t1, x0, [1.0]), [1.0]))
        if is_dual(out) and out.tag.ident == t2.ident:
            p, t = out.primal, out.tangent[0]
        else:
            p, t = out, 0.0
        return primal_value(p), _comp(p, 0, t1), _comp(t, 0, t1)

    return _run(budget, run)


def jvp(fn, x, directions, budget=None):
    """Batched Jacobian-vector product.

    ``fn`` maps a list of ``n`` inputs to a list of ``m`` outputs (a single
    scalar output is also accepted).  ``directions`` is a list of ``k``
    direction vectors of length ``n``; all of them are pushed through in
    one pass.  Returns ``(primals, pushforwards)`` where ``primals`` has
    length ``m`` and ``pushforwards[k']`` is ``J @ directions[k']``.
    """
    x = list(x)
    directions = [list(d) for d in directions]
    for d in directions:
        if len(d) != len(x):
            raise ValueError("direction length must match the input length")
    tag = fresh_tag(len(directions))

    def run():
        xd = [Dual(tag, xi, [d[i] for d in directions]) for i, xi in enumerate(x)]
        out = fn(xd)
        outs = list(out) if isinstance(out, (list, tuple)) else [out]
        primals = [primal_value(o) for o in outs]
        push = [[_comp(o, k, tag) for o in outs] for k in range(len(directions))]
        return primals, push

    return _run(budget, run)


def jacobian(fn, x, budget=None):
    """Full Jacobian of a vector function.  Returns ``(primals, J)`` with
    ``J[output][input]``."""
    x = list(x)
    primals, cols = jvp(fn, x, _identity(len(x)), budget)
    J = [[primal_value(cols[j][i]) for j in range(len(x))]
         for i in range(len(primals))]
    return primals, J


def gradient(fn, x, budget=None):
    """Gradient of a scalar function.  Returns ``(value, grad)``."""
    x = list(x)
    primals, cols = jvp(fn, x, _identity(len(x)), budget)
    return primals[0], [primal_value(cols[i][0]) for i in range(len(x))]


def mixed_partial(fn, x, i, j, budget=None):
    """Second mixed partial ``d^2 f / dx_i dx_j`` of a scalar function.

    Uses two nested tags (one per direction); the coefficient of the
    cross perturbation ``e_i * e_j`` is the mixed partial.
    """
    ti = fresh_tag(1)
    tj = fresh_tag(1)

    def run():
        xd = []
        for k, xk in enumerate(x):
            v = xk
            if k == i:
                v = Dual(ti, v, [1.0])
            if k == j:
                v = Dual(tj, v, [1.0])
            xd.append(v)
        out = fn(xd)
        return primal_value(_comp(_comp(out, 0, tj), 0, ti))

    return _run(budget, run)


def hessian(fn, x, budget=None):
    """Gradient and Hessian of a scalar function in one pass.

    Seeds an outer tag with the identity in ``x`` and an inner tag with the
    identity again; the tangent-of-tangent block is the Hessian.
    Returns ``(value, grad, H)`` with ``H[j][l] = d^2f / dx_j dx_l``.
    """
    x = list(x)
    n = len(x)
    ti = fresh_tag(n)
    tj = fresh_tag(n)

    def run():
        xd = []
        for k in range(n):
            ek = [1.0 if l == k else 0.0 for l in range(n)]
            xd.append(Dual(tj, Dual(ti, x[k], ek), list(ek)))
        out = fn(xd)
        p = out.primal if (is_dual(out) and out.tag.ident == tj.ident) else out
        grad = [primal_value(_comp(p, l, ti)) for l in range(n)]
        H = [[primal_value(_comp(_comp(out, j, tj), l, ti)) for l in range(n)]
             for j in range(n)]
        return primal_value(p), grad, H

    return _run(budget, run)
