"""Tagged dual numbers with perturbation-confusion isolation.

Every differentiation invocation allocates a fresh :class:`Tag`.  When two
values carrying different tags meet, the computation proceeds in the
*newer* (inner) tag and the older value is embedded as a constant whose own
perturbation rides along inside the primal.  This is the standard
tag-discipline answer to perturbation confusion (Siskind & Pearlmutter):
an inner derivative may reference outer, still-perturbed variables without
the two perturbations ever being merged.

A ``Dual`` carries ``tag.ndir`` tangent components, so one tag can push
several directions through a computation in a single pass (batched JVP).
Both the primal and each tangent component may themselves be ``Dual`` of an
*older* tag; this nesting is what makes higher-order and mixed derivatives
work.
"""
from __future__ import annotations

import math

from .budget import get_budget
from .errors import DomainError, NonDifferentiableError


class Tag:
    """Identifies one differentiation invocation.

    ``ident`` is globally unique and monotonically increasing; the *newer*
    tag (larger ident) is the *inner* differentiation.  ``ndir`` is the
    number of simultaneous directional seeds.
    """

    __slots__ = ("ident", "ndir")

    def __init__(self, ident, ndir):
        self.ident = ident
        self.ndir = ndir

    def __repr__(self):
        return f"Tag(ident={self.ident}, ndir={self.ndir})"


_tag_counter = 0


def fresh_tag(ndir=1):
    """Allocate a globally unique tag.

    Ids are never reused, even if the computation that requested the tag is
    cancelled midway, so stale state can never alias a later computation.
    """
    global _tag_counter
    _tag_counter += 1
    return Tag(_tag_counter, ndir)


def tags_issued():
    """How many tags have been allocated so far (monotonic)."""
    return _tag_counter


class Dual:
    """A primal value plus ``tag.ndir`` tangent components."""

    __slots__ = ("tag", "primal", "tangent")

    def __init__(self, tag, primal, tangent):
        get_budget().alloc()
        self.tag = tag
        self.primal = primal
        self.tangent = list(tangent)

    def __repr__(self):
        return (f"Dual(tag={self.tag.ident}, primal={self.primal!r}, "
                f"tangent={self.tangent!r})")

    def __add__(self, other):
        return add(self, other)

    def __radd__(self, other):
        return add(other, self)

    def __sub__(self, other):
        return sub(self, other)

    def __rsub__(self, other):
        return sub(other, self)

    def __mul__(self, other):
        return mul(self, other)

    def __rmul__(self, other):
        return mul(other, self)

    def __truediv__(self, other):
        return div(self, other)

    def __rtruediv__(self, other):
        return div(other, self)

    def __pow__(self, other):
        return power(self, other)

    def __rpow__(self, other):
        return power(other, self)

    def __neg__(self):
        return neg(self)

    def __abs__(self):
        return abs_(self)


def is_dual(x):
    return isinstance(x, Dual)


def primal_value(x):
    """Fully unwrap nested duals down to the base (tag-free) value."""
    while isinstance(x, Dual):
        x = x.primal
    return x


def _zeros(tag):
    return [0.0] * tag.ndir


def _lift(x, tag):
    """View ``x`` as a constant in ``tag``'s differentiation.

    A dual of an *older* tag is embedded whole: its own perturbation is
    preserved inside the primal, which is exactly what prevents
    perturbation confusion.
    """
    if isinstance(x, Dual):
        if x.tag.ident == tag.ident:
            return x
        if x.tag.ident > tag.ident:
            raise RuntimeError(
                "tag order violation: cannot embed an inner tag inside an outer one")
    return Dual(tag, x, _zeros(tag))


def _common_tag(a, b):
    ta = a.tag if isinstance(a, Dual) else None
    tb = b.tag if isinstance(b, Dual) else None
    if ta is None:
        return tb
    if tb is None:
        return ta
    return ta if ta.ident >= tb.ident else tb


def add(a, b):
    tag = _common_tag(a, b)
    if tag is None:
        return a + b
    get_budget().tick()
    a = _lift(a, tag)
    b = _lift(b, tag)
    return Dual(tag, add(a.primal, b.primal),
                [add(x, y) for x, y in zip(a.tangent, b.tangent)])


def neg(a):
    if not isinstance(a, Dual):
        return -a
    get_budget().tick()
    return Dual(a.tag, neg(a.primal), [neg(t) for t in a.tangent])


def sub(a, b):
    return add(a, neg(b))


def mul(a, b):
    tag = _common_tag(a, b)
    if tag is None:
        return a * b
    get_budget().tick()
    a = _lift(a, tag)
    b = _lift(b, tag)
    pa, pb = a.primal, b.primal
    return Dual(tag, mul(pa, pb),
                [add(mul(pa, tb_i), mul(ta_i, pb))
                 for ta_i, tb_i in zip(a.tangent, b.tangent)])


def div(a, b):
    tag = _common_tag(a, b)
    if tag is None:
        return a / b
    get_budget().tick()
    a = _lift(a, tag)
    b = _lift(b, tag)
    pa, pb = a.primal, b.primal
    qq = mul(pb, pb)
    return Dual(tag, div(pa, pb),
                [div(sub(mul(ta_i, pb), mul(pa, tb_i)), qq)
                 for ta_i, tb_i in zip(a.tangent, b.tangent)])


def recip(x):
    return div(1.0, x)


def power(a, b):
    if not isinstance(b, Dual):
        if isinstance(b, bool):
            b = int(b)
        if isinstance(b, int):
            return _pow_int(a, b)
        if isinstance(b, float) and b.is_integer():
            return _pow_int(a, int(b))
        base = primal_value(a)
        if base < 0:
            raise DomainError(f"negative base {base} with real exponent {b}")
        if base == 0:
            if b > 1:
                return 0.0  # value and derivative are both 0
            raise NonDifferentiableError(
                f"x^{b} is not differentiable at x = 0")
        return exp(mul(b, log(a)))
    if primal_value(a) <= 0:
        raise DomainError("a**b with a varying exponent requires a > 0")
    return exp(mul(b, log(a)))


def _pow_int(a, n):
    if n < 0:
        return div(1.0, _pow_int(a, -n))
    if n == 0:
        return 1.0
    if not isinstance(a, Dual):
        return a ** n
    get_budget().tick()
    factor = _pow_int(a.primal, n - 1)
    return Dual(a.tag, _pow_int(a.primal, n),
                [mul(mul(float(n), factor), ti) for ti in a.tangent])


def _unary(x, f_plain, f_rec, df_rec):
    if not isinstance(x, Dual):
        return f_plain(x)
    get_budget().tick()
    p = x.primal
    d = df_rec(p)
    return Dual(x.tag, f_rec(p), [mul(d, ti) for ti in x.tangent])


def sin(x):
    return _unary(x, math.sin, sin, cos)


def cos(x):
    return _unary(x, math.cos, cos, lambda u: neg(sin(u)))


def tan(x):
    return _unary(x, math.tan, tan, lambda u: recip(_pow_int(cos(u), 2)))


def exp(x):
    return _unary(x, math.exp, exp, exp)


def log(x):
    if primal_value(x) <= 0:
        raise DomainError(f"log of non-positive value {primal_value(x)}")
    return _unary(x, math.log, log, recip)


def sqrt(x):
    v = primal_value(x)
    if v < 0:
        raise DomainError(f"sqrt of negative value {v}")
    if v == 0:
        raise NonDifferentiableError("sqrt is not differentiable at 0")
    return _unary(x, math.sqrt, sqrt, lambda u: div(0.5, sqrt(u)))


def abs_(x):
    if primal_value(x) == 0:
        raise NonDifferentiableError("abs is not differentiable at 0")
    return _unary(x, abs, abs_, sign)


def sign(x):
    v = primal_value(x)
    if v > 0:
        return 1.0
    if v < 0:
        return -1.0
    raise NonDifferentiableError("sign is not differentiable at 0")


def min_(a, b):
    fa, fb = primal_value(a), primal_value(b)
    if fa == fb:
        raise NonDifferentiableError("min hit a kink (equal branches)")
    return a if fa < fb else b


def max_(a, b):
    fa, fb = primal_value(a), primal_value(b)
    if fa == fb:
        raise NonDifferentiableError("max hit a kink (equal branches)")
    return a if fa > fb else b
