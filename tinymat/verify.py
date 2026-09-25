"""Independent numerical verification utilities (finite differences).

Differentiability assessment
----------------------------
``matmul`` is a bilinear (hence polynomial, C^infinity) map in both
operands, including all batched/broadcast/vector-promoted forms: promotion
only inserts size-1 axes and broadcasting only reuses memory.  There are
therefore NO non-differentiable points; the k=0 inner-dimension case is the
constant zero map, which is smooth as well.  Any discrepancy between an
analytic derivative and a finite difference must thus be classified as
either

* ``"numerical"`` -- consistent with the finite-difference error model
  (truncation + floating-point roundoff), or
* ``"genuine"``   -- exceeds the model, i.e. a real bug (or, for other
  operators, a non-differentiable point).

Error model (central differences, machine epsilon eps ~ 2.2e-16):
* first derivative:  err ~ C2 * h^2 * |f'''|  +  eps * |f| / h
* second derivative: err ~ C4 * h^2 * |f^(4)| +  4 * eps * |f| / h^2
"""

import math

from .tensor import iter_indices

MACHINE_EPS = 2.0 ** -52


def physical_cells_of(*tensors):
    """Unique (buffer_id, buffer, index) cells touched by the tensors."""
    seen = {}
    for t in tensors:
        for idx in iter_indices(t.shape):
            p = t.offset + sum(i * s for i, s in zip(idx, t.strides))
            seen[(id(t.data), p)] = (t.data, p)
    return sorted(seen.values(), key=lambda bp: bp[1])


def fd_grad_physical(f, cells, h=1e-5):
    """Central-difference gradient of scalar f() w.r.t. physical cells.

    ``cells`` is a list of (buffer, index); f must read through the views
    so that perturbing a shared cell is seen by every logical element.
    """
    grads = []
    for buf, p in cells:
        orig = buf[p]
        buf[p] = orig + h
        fp = f()
        buf[p] = orig - h
        fm = f()
        buf[p] = orig
        grads.append((fp - fm) / (2.0 * h))
    return grads


def fd_first_directional(f_at, h=1e-5):
    """(f(h) - f(-h)) / 2h where f_at(t) evaluates at x + t*d."""
    return (f_at(h) - f_at(-h)) / (2.0 * h)


def fd_second_directional(f_at, h=1e-3):
    """(f(h) - 2f(0) + f(-h)) / h^2 ~ d^T H d."""
    return (f_at(h) - 2.0 * f_at(0.0) + f_at(-h)) / (h * h)


def perturb(t, direction, scale):
    """t += scale * direction along logical indices (requires an injective
    logical->physical map, e.g. contiguous or non-overlapping strided t)."""
    for idx in iter_indices(t.shape):
        p = t.offset + sum(i * s for i, s in zip(idx, t.strides))
        t.data[p] += scale * direction.value_at(idx)


def classify_discrepancy(analytic, estimate, trunc_bound, round_bound,
                         factor=50.0):
    """Classify |analytic - estimate| against the FD error model.

    Returns ("numerical", err, bound) if the discrepancy is explained by
    truncation + roundoff (up to a conservative slack factor), else
    ("genuine", err, bound).
    """
    err = abs(analytic - estimate)
    scale = max(1.0, abs(analytic), abs(estimate))
    bound = factor * (trunc_bound + round_bound) * scale
    return ("numerical" if err <= bound else "genuine", err, bound)


def first_derivative_bounds(f_scale, h):
    """(truncation, roundoff) bounds for a central first derivative."""
    return f_scale * h * h, MACHINE_EPS * f_scale / h


def second_derivative_bounds(f_scale, h):
    """(truncation, roundoff) bounds for a central second derivative."""
    return f_scale * h * h, 4.0 * MACHINE_EPS * f_scale / (h * h)
