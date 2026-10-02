"""2D affine coordinate transforms with hand-written gradients.

A transform is a 6-tuple ``(a, b, c, d, e, f)`` mapping a point ``(x, y)`` to

    u = a*x + b*y + c
    v = d*x + e*y + f

Composition ``affine_compose(t2, t1)`` applies ``t1`` first, then ``t2``.
Singular transforms (zero determinant) are valid inputs: forward still maps
points onto a line/point, gradients w.r.t. input coordinates collapse onto
the row space (zero for a rank-0 map), and gradients w.r.t. the parameters
remain well defined.
"""


def affine_apply(params, points):
    """Apply the transform to a list of ``(x, y)`` points."""
    a, b, c, d, e, f = params
    return [(a * x + b * y + c, d * x + e * y + f) for x, y in points]


def affine_backward(params, points, grad_out):
    """Backward of :func:`affine_apply`.

    ``grad_out`` is a list of ``(grad_u, grad_v)`` per point.  Returns
    ``(grad_points, grad_params)`` with contributions of all points
    accumulated into ``grad_params``.
    """
    a, b, c, d, e, f = params
    if len(grad_out) != len(points):
        raise ValueError("grad_out length does not match number of points")
    grad_points = []
    ga = gb = gc = gd = ge = gf = 0.0
    for (x, y), (gu, gv) in zip(points, grad_out):
        grad_points.append((gu * a + gv * d, gu * b + gv * e))
        ga += gu * x
        gb += gu * y
        gc += gu
        gd += gv * x
        ge += gv * y
        gf += gv
    return grad_points, (ga, gb, gc, gd, ge, gf)


def affine_compose(t2, t1):
    """Return the single transform equivalent to ``t2(t1(x))``."""
    a2, b2, c2, d2, e2, f2 = t2
    a1, b1, c1, d1, e1, f1 = t1
    return (
        a2 * a1 + b2 * d1,
        a2 * b1 + b2 * e1,
        a2 * c1 + b2 * f1 + c2,
        d2 * a1 + e2 * d1,
        d2 * b1 + e2 * e1,
        d2 * c1 + e2 * f1 + f2,
    )
