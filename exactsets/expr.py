"""Expression trees: comparisons of rational functions + boolean operators."""
from __future__ import annotations

from fractions import Fraction

from . import polys as P

OPS = ("<", "<=", "=", "!=", ">=", ">")


def op_truth(op, s):
    """Truth of the comparison ``s <op> 0`` where s in {-1, 0, 1}."""
    if op == "<":
        return s < 0
    if op == "<=":
        return s <= 0
    if op == ">":
        return s > 0
    if op == ">=":
        return s >= 0
    if op == "=":
        return s == 0
    if op == "!=":
        return s != 0
    raise ValueError(op)


class Expr:
    def __and__(self, other):
        return BoolOp("and", (self, other))

    def __or__(self, other):
        return BoolOp("or", (self, other))

    def __invert__(self):
        return BoolOp("not", (self,))


class Atom(Expr):
    """Comparison ``num/den <op> 0`` with the original, unsimplified den."""

    __slots__ = ("num", "den", "op", "index")

    def __init__(self, num, den, op):
        if op not in OPS:
            raise ValueError(op)
        self.num = P.norm(num)
        self.den = P.norm(den)
        self.op = op
        self.index = -1

    def __repr__(self):
        return f"Atom(({P.poly_to_str(self.num)})/({P.poly_to_str(self.den)}) {self.op} 0)"


class BoolOp(Expr):
    __slots__ = ("kind", "children")

    def __init__(self, kind, children):
        assert kind in ("and", "or", "not")
        self.kind = kind
        self.children = tuple(children)

    def __repr__(self):
        return f"BoolOp({self.kind}, {self.children})"


def iter_atoms(e):
    if isinstance(e, Atom):
        yield e
    elif isinstance(e, BoolOp):
        for c in e.children:
            yield from iter_atoms(c)
    else:
        raise TypeError(e)


class Rat:
    """A rational function ``num/den`` used to build comparison atoms.

    The denominator is kept exactly as written: its real zeros are excluded
    from the common domain by the solver, and no cancellation ever restores
    those points.
    """

    __slots__ = ("num", "den")

    def __init__(self, num, den=None):
        self.num = P.norm(num)
        self.den = P.norm(den if den is not None else [1])

    @staticmethod
    def _coerce(o):
        if isinstance(o, Rat):
            return o
        if isinstance(o, (int, Fraction)):
            return Rat([o])
        if isinstance(o, (list, tuple)):
            return Rat(o)
        raise TypeError(o)

    def __add__(self, o):
        o = Rat._coerce(o)
        return Rat(
            P.add(P.mul(self.num, o.den), P.mul(o.num, self.den)),
            P.mul(self.den, o.den),
        )

    __radd__ = __add__

    def __sub__(self, o):
        o = Rat._coerce(o)
        return Rat(
            P.sub(P.mul(self.num, o.den), P.mul(o.num, self.den)),
            P.mul(self.den, o.den),
        )

    def __rsub__(self, o):
        return Rat._coerce(o).__sub__(self)

    def _atom(self, o, op):
        d = self - Rat._coerce(o)
        return Atom(d.num, d.den, op)

    def __lt__(self, o):
        return self._atom(o, "<")

    def __le__(self, o):
        return self._atom(o, "<=")

    def __gt__(self, o):
        return self._atom(o, ">")

    def __ge__(self, o):
        return self._atom(o, ">=")

    def eq(self, o):
        return self._atom(o, "=")

    def ne(self, o):
        return self._atom(o, "!=")
