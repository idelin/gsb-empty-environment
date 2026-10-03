"""Differentiation drivers built on tagged dual numbers.

Every public entry point creates fresh Tags, so calls nest freely: an
inner derivative never sees the outer perturbation (no perturbation
confusion), and mixed second derivatives are read off by extracting one
tag's tangent from inside another tag's tangent.
"""
from __future__ import annotations

from .dual import Dual, primal_of
from .tags import Tag, tag_scope

__all__ = [
    "jvp", "derivative", "gradient", "jacobian",
    "second_derivative", "mixed_directional", "mixed_partial", "hessian",
]


def _seed(x, tag, directions):
    return Dual(float(x), {tag: tuple(float(d) for d in directions)})


def _extract(y, tag, n):
    if isinstance(y, Dual):
        tangents = y.partials.get(tag)
        if tangents is not None:
            return tangents
    return (0.0,) * n


def _split(y, tag, n):
    if isinstance(y, (list, tuple)):
        return ([primal_of(v) for v in y],
                [_extract(v, tag, n) for v in y])
    return primal_of(y), _extract(y, tag, n)


def jvp(f, x, directions=(1.0,), tag=None):
    """Jacobian-vector product at scalar x for several directions at once.

    Returns (primal, tangents); tangents[i] corresponds to directions[i].
    If f returns a list/tuple, both parts are lists.
    """
    tag = tag if tag is not None else Tag("jvp")
    dirs = tuple(float(d) for d in directions)
    with tag_scope(tag):
        y = f(_seed(x, tag, dirs))
        return _split(y, tag, len(dirs))


def derivative(f, x):
    """df/dx at x. Safe to nest: each call uses a fresh tag."""
    _, tangents = jvp(f, x, (1.0,))
    return tangents[0]


def gradient(f, args):
    """Gradient of scalar f at point args; f is called as f(*args)."""
    args = [float(a) for a in args]
    tags = [Tag(f"grad{i}") for i in range(len(args))]
    with tag_scope(*tags):
        seeded = [Dual(a, {t: (1.0,)}) for a, t in zip(args, tags)]
        y = f(*seeded)
        return primal_of(y), [_extract(y, t, 1)[0] for t in tags]


def jacobian(f, xs):
    """Jacobian of a vector function at xs.

    f receives the seeded list and must return a list. Uses a single tag
    with len(xs) simultaneous directions (multi-directional JVP).
    Returns (primals, matrix) with matrix[row][col] = d f_row / d x_col.
    """
    xs = [float(v) for v in xs]
    n = len(xs)
    tag = Tag("jacobian")
    with tag_scope(tag):
        seeded = [
            Dual(v, {tag: tuple(1.0 if j == i else 0.0 for j in range(n))})
            for i, v in enumerate(xs)
        ]
        ys = f(seeded)
        primals = [primal_of(v) for v in ys]
        matrix = [[_extract(v, tag, n)[j] for j in range(n)] for v in ys]
        return primals, matrix


def second_derivative(f, x):
    """d2f/dx2 at scalar x via two nested tags."""
    t1, t2 = Tag("second.outer"), Tag("second.inner")
    with tag_scope(t1, t2):
        xd = Dual(Dual(float(x), {t1: (1.0,)}), {t2: (1.0,)})
        yd = f(xd)
        first = _extract(yd, t2, 1)[0]
        return _extract(first, t1, 1)[0]


def mixed_directional(f, x, u, v):
    """d2/dsdt f(x + s*u + t*v) at s=t=0, i.e. u^T H v for scalar input."""
    t1, t2 = Tag("dir.u"), Tag("dir.v")
    with tag_scope(t1, t2):
        xd = Dual(Dual(float(x), {t1: (float(u),)}), {t2: (float(v),)})
        yd = f(xd)
        first = _extract(yd, t2, 1)[0]
        return _extract(first, t1, 1)[0]


def mixed_partial(f, args, i, j):
    """d2f/(d args[i] d args[j]) at args; f is called as f(*args)."""
    ti, tj = Tag(f"d{i}"), Tag(f"d{j}")
    with tag_scope(ti, tj):
        seeded = []
        for k, a in enumerate(args):
            a = float(a)
            if k == i and k == j:
                seeded.append(Dual(Dual(a, {ti: (1.0,)}), {tj: (1.0,)}))
            elif k == i:
                seeded.append(Dual(Dual(a, {ti: (1.0,)}), {}))
            elif k == j:
                seeded.append(Dual(a, {tj: (1.0,)}))
            else:
                seeded.append(a)
        yd = f(*seeded)
        first = _extract(yd, tj, 1)[0]
        return _extract(first, ti, 1)[0]


def hessian(f, args):
    """Full Hessian of scalar f at args (small problems only)."""
    n = len(args)
    return [[mixed_partial(f, args, i, j) for j in range(n)] for i in range(n)]
