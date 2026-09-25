"""Independent finite-difference gradient checking.

The analytical engine and this checker share only the forward kernels, never
the backward rules.  Each scalar coordinate of a leaf is perturbed with a
central difference and compared against the gradient produced by ``backward``.
"""

from __future__ import annotations

from .graph import Graph, no_grad


def _scalarize(value):
    if hasattr(value, "shape") and value.shape != ():
        raise ValueError("gradcheck requires a scalar-valued function")
    return value.item() if hasattr(value, "item") else float(value)


def numerical_grad(fn, leaves, eps=1e-5):
    """Central-difference gradient of scalar ``fn()`` w.r.t. every leaf."""
    result = []
    # Numerical sweeps never build history: suspend recording but still provide
    # a scratch graph so requires_grad tensors are legal inside build_fn.
    with no_grad(), Graph(name="numeric-eval").use():
        for leaf in leaves:
            grad = []
            for i in range(len(leaf.data)):
                orig = leaf.data[i]
                leaf.data[i] = orig + eps
                plus = _scalarize(fn())
                leaf.data[i] = orig - eps
                minus = _scalarize(fn())
                leaf.data[i] = orig
                grad.append((plus - minus) / (2.0 * eps))
            result.append(grad)
    return result


def analytic_grad(leaves):
    return [list(t.grad.data) if t.grad is not None else [0.0] * len(t.data)
            for t in leaves]


def max_abs_rel_error(analytic, numeric, eps_rel=1e-8):
    worst = 0.0
    for av, nv in zip(analytic, numeric):
        for a, n in zip(av, nv):
            denom = max(1.0, abs(a), abs(n))
            worst = max(worst, abs(a - n) / (denom + eps_rel))
    return worst


def gradcheck(build_fn, leaves, eps=1e-5, tol=1e-4, verbose=False):
    """Rebuild the graph, run analytical backward, and compare numerically.

    ``build_fn`` must return a fresh scalar tensor each call.  Returns the
    worst relative error; raises ``AssertionError`` if it exceeds ``tol``.
    """
    for leaf in leaves:
        leaf.zero_grad()
    with Graph(name="gradcheck").use():
        scalar = build_fn()
    scalar.backward()
    got = analytic_grad(leaves)

    expected = numerical_grad(build_fn, leaves, eps=eps)
    err = max_abs_rel_error(got, expected)
    if verbose:
        print(f"analytic={got}\nnumeric ={expected}\nrel_err ={err:.3e}")
    assert err < tol, f"gradcheck failed: rel error {err:.3e} >= {tol}"
    return err
