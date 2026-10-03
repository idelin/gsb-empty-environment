"""Tagged dual numbers with multi-directional tangents.

A Dual carries a primal (a float, or another Dual when differentiations
are nested) plus a mapping Tag -> tuple of tangent components, one tuple
entry per seed direction. Tags isolate perturbations between nested
differentiation contexts, which is what prevents perturbation confusion.

No external dependencies; plain floats and Python arithmetic only.
"""
from __future__ import annotations

import math

from . import budget as _budget
from .errors import DomainError, NonDifferentiableError

__all__ = [
    "Dual", "is_dual", "primal_of", "partials_of", "deep_primal", "scalar_of",
    "add", "sub", "mul", "truediv", "pow_", "neg",
    "sin", "cos", "tan", "exp", "log", "sqrt",
    "dual_abs", "dual_min", "dual_max",
]


def is_dual(x):
    return isinstance(x, Dual)


def primal_of(x):
    return x.primal if isinstance(x, Dual) else x


def partials_of(x):
    return x.partials if isinstance(x, Dual) else {}


def deep_primal(x):
    """Strip all nesting levels down to a plain float."""
    while isinstance(x, Dual):
        x = x.primal
    return float(x)


def scalar_of(x):
    """Like deep_primal but refuses to silently drop live tangents."""
    if isinstance(x, Dual):
        if x.partials:
            raise TypeError("cannot convert a Dual with live tangents to float")
        return scalar_of(x.primal)
    return float(x)


class Dual:
    __slots__ = ("primal", "partials", "_budget", "_slots")

    def __init__(self, primal, partials=None):
        self._budget = None
        self._slots = 0
        self.primal = primal
        self.partials = dict(partials) if partials else {}
        slots = sum(len(v) for v in self.partials.values())
        if slots:
            active = _budget.current()
            if active is not None:
                active.alloc(slots)
                self._budget = active
                self._slots = slots

    def __del__(self):
        active = getattr(self, "_budget", None)
        if active is not None:
            try:
                active.release(self._slots)
            except Exception:
                pass

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
        return truediv(self, other)

    def __rtruediv__(self, other):
        return truediv(other, self)

    def __pow__(self, other):
        return pow_(self, other)

    def __rpow__(self, other):
        return pow_(other, self)

    def __neg__(self):
        return neg(self)

    def __pos__(self):
        return self

    def __abs__(self):
        return dual_abs(self)

    @staticmethod
    def _comparable(other):
        return isinstance(other, (int, float, Dual))

    def __eq__(self, other):
        if not self._comparable(other):
            return NotImplemented
        return deep_primal(self) == deep_primal(other)

    def __ne__(self, other):
        result = self.__eq__(other)
        return result if result is NotImplemented else not result

    def __lt__(self, other):
        if not self._comparable(other):
            return NotImplemented
        return deep_primal(self) < deep_primal(other)

    def __le__(self, other):
        if not self._comparable(other):
            return NotImplemented
        return deep_primal(self) <= deep_primal(other)

    def __gt__(self, other):
        if not self._comparable(other):
            return NotImplemented
        return deep_primal(self) > deep_primal(other)

    def __ge__(self, other):
        if not self._comparable(other):
            return NotImplemented
        return deep_primal(self) >= deep_primal(other)

    __hash__ = None

    def __float__(self):
        return scalar_of(self)

    def __repr__(self):
        return f"Dual(primal={self.primal!r}, partials={self.partials!r})"


def _strip_tag(x, tag):
    """Remove one tag's tangents from x.

    Derivative factors for tag t must be evaluated on operands stripped of
    t (each tag is an independent first-order nilpotent direction), while
    keeping every *other* tag's tangents intact so nested differentiation
    and mixed partials propagate correctly.
    """
    if isinstance(x, Dual) and tag in x.partials:
        rest = {k: v for k, v in x.partials.items() if k is not tag}
        if not rest:
            return x.primal
        return Dual(x.primal, rest)
    return x


def _combine(a, b, f, dfa, dfb):
    pa = primal_of(a)
    pb = primal_of(b)
    da = partials_of(a)
    db = partials_of(b)
    primal = f(pa, pb)
    if not da and not db:
        return primal
    partials = {}
    for tag in da.keys() | db.keys():
        ta = da.get(tag)
        tb = db.get(tag)
        n = max(len(ta) if ta is not None else 0, len(tb) if tb is not None else 0)
        ca = dfa(_strip_tag(a, tag), _strip_tag(b, tag)) if ta is not None else None
        cb = dfb(_strip_tag(a, tag), _strip_tag(b, tag)) if tb is not None else None
        if ta is None:
            ta = (0.0,) * n
        if tb is None:
            tb = (0.0,) * n
        out = []
        for i in range(n):
            term = 0.0
            if ca is not None:
                term = ca * ta[i]
            if cb is not None:
                term = term + cb * tb[i]
            out.append(term)
        partials[tag] = tuple(out)
    return Dual(primal, partials)


def _unary(a, f, df):
    pa = primal_of(a)
    da = partials_of(a)
    primal = f(pa)
    if not da:
        return primal
    partials = {}
    for tag, vals in da.items():
        c = df(_strip_tag(a, tag))
        partials[tag] = tuple(c * v for v in vals)
    return Dual(primal, partials)


def add(a, b):
    _budget.tick()
    return _combine(a, b, lambda pa, pb: pa + pb,
                    lambda pa, pb: 1.0, lambda pa, pb: 1.0)


def sub(a, b):
    _budget.tick()
    return _combine(a, b, lambda pa, pb: pa - pb,
                    lambda pa, pb: 1.0, lambda pa, pb: -1.0)


def mul(a, b):
    _budget.tick()
    return _combine(a, b, lambda pa, pb: pa * pb,
                    lambda pa, pb: pb, lambda pa, pb: pa)


def truediv(a, b):
    _budget.tick()
    return _combine(a, b, lambda pa, pb: pa / pb,
                    lambda pa, pb: 1.0 / pb,
                    lambda pa, pb: -pa / (pb * pb))


def pow_(a, b):
    _budget.tick()

    def dfa(x, y):
        return y * (x ** (y - 1))

    def dfb(x, y):
        if deep_primal(x) <= 0.0:
            raise DomainError("x**y with a variable exponent requires x > 0")
        return (x ** y) * log(x)

    return _combine(a, b, lambda pa, pb: pa ** pb, dfa, dfb)


def neg(a):
    _budget.tick()
    return _unary(a, lambda p: -p, lambda p: -1.0)


def _lift(math_fn, dual_fn):
    return lambda p: dual_fn(p) if isinstance(p, Dual) else math_fn(p)


def exp(a):
    _budget.tick()
    return _unary(a, _lift(math.exp, exp), _lift(math.exp, exp))


def sin(a):
    _budget.tick()
    return _unary(a, _lift(math.sin, sin), _lift(math.cos, cos))


def cos(a):
    _budget.tick()
    return _unary(a, _lift(math.cos, cos),
                  lambda p: neg(sin(p)) if isinstance(p, Dual) else -math.sin(p))


def tan(a):
    _budget.tick()

    def df(p):
        c = cos(p)
        return 1.0 / (c * c)

    return _unary(a, _lift(math.tan, tan), df)


def log(a):
    _budget.tick()
    if deep_primal(a) <= 0.0:
        raise DomainError("log requires a positive argument")
    return _unary(a, _lift(math.log, log), lambda p: 1.0 / p)


def sqrt(a):
    _budget.tick()
    p = deep_primal(a)
    if p < 0.0:
        raise DomainError("sqrt requires a non-negative argument")
    if p == 0.0 and partials_of(a):
        raise NonDifferentiableError("sqrt is not differentiable at 0")
    lifted = _lift(math.sqrt, sqrt)
    return _unary(a, lifted, lambda q: 0.5 / lifted(q))


def dual_abs(a):
    _budget.tick()
    p = deep_primal(a)
    if p > 0.0:
        return a
    if p < 0.0:
        return neg(a)
    if partials_of(a):
        raise NonDifferentiableError("abs is not differentiable at 0")
    return 0.0


def dual_min(a, b):
    _budget.tick()
    pa, pb = deep_primal(a), deep_primal(b)
    if pa == pb and (partials_of(a) or partials_of(b)):
        raise NonDifferentiableError("min is not differentiable at a tie")
    return a if pa <= pb else b


def dual_max(a, b):
    _budget.tick()
    pa, pb = deep_primal(a), deep_primal(b)
    if pa == pb and (partials_of(a) or partials_of(b)):
        raise NonDifferentiableError("max is not differentiable at a tie")
    return a if pa >= pb else b
