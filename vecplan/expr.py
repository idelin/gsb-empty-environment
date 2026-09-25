"""Expression AST. Build with col()/lit() helpers or operator overloading."""
from __future__ import annotations

ARITH_OPS = ("add", "sub", "mul", "div", "mod")
COMPARE_OPS = ("eq", "ne", "lt", "le", "gt", "ge")
LOGIC_OPS = ("and", "or")


class Expr:
    """Base class with operator overloads for ergonomic expression building."""

    def __add__(self, other): return Binary("add", self, ensure(other))
    def __radd__(self, other): return Binary("add", ensure(other), self)
    def __sub__(self, other): return Binary("sub", self, ensure(other))
    def __rsub__(self, other): return Binary("sub", ensure(other), self)
    def __mul__(self, other): return Binary("mul", self, ensure(other))
    def __rmul__(self, other): return Binary("mul", ensure(other), self)
    def __truediv__(self, other): return Binary("div", self, ensure(other))
    def __rtruediv__(self, other): return Binary("div", ensure(other), self)
    def __mod__(self, other): return Binary("mod", self, ensure(other))
    def __rmod__(self, other): return Binary("mod", ensure(other), self)
    def __eq__(self, other): return Binary("eq", self, ensure(other))
    def __ne__(self, other): return Binary("ne", self, ensure(other))
    def __lt__(self, other): return Binary("lt", self, ensure(other))
    def __le__(self, other): return Binary("le", self, ensure(other))
    def __gt__(self, other): return Binary("gt", self, ensure(other))
    def __ge__(self, other): return Binary("ge", self, ensure(other))
    def __and__(self, other): return Binary("and", self, ensure(other))
    def __rand__(self, other): return Binary("and", ensure(other), self)
    def __or__(self, other): return Binary("or", self, ensure(other))
    def __ror__(self, other): return Binary("or", ensure(other), self)
    def __invert__(self): return Not(self)


class Literal(Expr):
    def __init__(self, value, type=None):
        self.value = value
        self.type = type

    def __repr__(self):
        return f"lit({self.value!r})"


class Column(Expr):
    def __init__(self, name):
        self.name = name

    def __repr__(self):
        return f"col({self.name!r})"


class Binary(Expr):
    def __init__(self, op, left, right):
        self.op = op
        self.left = left
        self.right = right

    def __repr__(self):
        return f"({self.left!r} {self.op} {self.right!r})"


class Not(Expr):
    def __init__(self, operand):
        self.operand = operand


class If(Expr):
    def __init__(self, cond, then, otherwise):
        self.cond = cond
        self.then = then
        self.otherwise = otherwise


class Cast(Expr):
    def __init__(self, expr, target):
        self.expr = expr
        self.target = target


class IsNull(Expr):
    def __init__(self, expr):
        self.expr = expr


def ensure(value):
    return value if isinstance(value, Expr) else Literal(value)


def col(name):
    return Column(name)


def lit(value, type=None):
    return Literal(value, type)


def if_(cond, then, otherwise):
    return If(ensure(cond), ensure(then), ensure(otherwise))


def cast(expr, target):
    return Cast(ensure(expr), target)


def and_(*exprs):
    return _fold("and", exprs)


def or_(*exprs):
    return _fold("or", exprs)


def _fold(op, exprs):
    if not exprs:
        raise ValueError(f"{op}_ needs at least one operand")
    result = ensure(exprs[0])
    for e in exprs[1:]:
        result = Binary(op, result, ensure(e))
    return result


def not_(expr):
    return Not(ensure(expr))


def is_null(expr):
    return IsNull(ensure(expr))
