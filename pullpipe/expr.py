"""最小表达式体系：列引用、字面量、比较/算术/逻辑（全部遵循 NULL 三值逻辑）。

表达式在绑定时解析列下标，执行时对单行 tuple 取值。
"""

from __future__ import annotations

from typing import Any, Sequence

from .null import Null, and3, arith3, cmp3, not3, or3
from .table import Schema


class Expr:
    def bind(self, schema: Schema) -> "Expr":
        return self

    def eval(self, row: tuple) -> Any:
        raise NotImplementedError

    # 运算符糖：col("v") > lit(5) 等价于 BinOp(">", ...)，自动遵循 3VL
    def __eq__(self, other):
        return BinOp("=", self, _as_expr(other))

    def __ne__(self, other):
        return BinOp("<>", self, _as_expr(other))

    def __lt__(self, other):
        return BinOp("<", self, _as_expr(other))

    def __le__(self, other):
        return BinOp("<=", self, _as_expr(other))

    def __gt__(self, other):
        return BinOp(">", self, _as_expr(other))

    def __ge__(self, other):
        return BinOp(">=", self, _as_expr(other))

    def __add__(self, other):
        return BinOp("+", self, _as_expr(other))

    def __sub__(self, other):
        return BinOp("-", self, _as_expr(other))

    def __mul__(self, other):
        return BinOp("*", self, _as_expr(other))

    def __truediv__(self, other):
        return BinOp("/", self, _as_expr(other))

    def __invert__(self):
        return UnaryNot(self)

    def __and__(self, other):
        return BinOp("AND", self, _as_expr(other))

    def __or__(self, other):
        return BinOp("OR", self, _as_expr(other))

    def __hash__(self):
        return id(self)


def _as_expr(value) -> "Expr":
    return value if isinstance(value, Expr) else Lit(value)


class Lit(Expr):
    __slots__ = ("value",)

    def __init__(self, value: Any):
        self.value = value

    def eval(self, row: tuple) -> Any:
        return self.value


class Col(Expr):
    def __init__(self, name: str):
        self.name = name
        self._idx: int | None = None

    def bind(self, schema: Schema) -> "Col":
        bound = Col(self.name)
        bound._idx = schema.index(self.name)
        return bound

    def eval(self, row: tuple) -> Any:
        if self._idx is None:
            raise RuntimeError("column expression not bound")
        return row[self._idx]


class UnaryNot(Expr):
    def __init__(self, inner: Expr):
        self.inner = inner

    def bind(self, schema: Schema) -> "UnaryNot":
        return UnaryNot(self.inner.bind(schema))

    def eval(self, row: tuple) -> Any:
        return not3(self.inner.eval(row))


class BinOp(Expr):
    def __init__(self, op: str, left: Expr, right: Expr):
        self.op = op
        self.left = left
        self.right = right

    def bind(self, schema: Schema) -> "BinOp":
        return BinOp(self.op, self.left.bind(schema), self.right.bind(schema))

    def eval(self, row: tuple) -> Any:
        op = self.op
        if op == "AND":
            return and3(self.left.eval(row), self.right.eval(row))
        if op == "OR":
            return or3(self.left.eval(row), self.right.eval(row))
        a = self.left.eval(row)
        b = self.right.eval(row)
        if op in ("=", "<>", "<", "<=", ">", ">="):
            return cmp3(a, op, b)
        if op in ("+", "-", "*", "/"):
            return arith3(a, op, b)
        raise ValueError(f"unsupported operator: {op}")


def col(name: str) -> Col:
    return Col(name)


def lit(value: Any) -> Lit:
    return Lit(value)


def not_(inner: Expr) -> UnaryNot:
    return UnaryNot(inner)


def and_(left: Expr, right: Expr) -> BinOp:
    return BinOp("AND", left, right)


def or_(left: Expr, right: Expr) -> BinOp:
    return BinOp("OR", left, right)


def bind_all(exprs: Sequence[Expr], schema: Schema) -> list[Expr]:
    return [e.bind(schema) for e in exprs]
