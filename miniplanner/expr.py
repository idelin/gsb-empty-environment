"""Predicate expressions used by the mini query planner.

The expression tree deliberately contains no SQL parser or database engine.
Parameters are referenced by position and retain their identity in cached plans.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Literal as T_Literal


@dataclass(frozen=True)
class Expr:
    """Base expression marker."""


@dataclass(frozen=True)
class ColRef(Expr):
    name: str


@dataclass(frozen=True)
class Param(Expr):
    index: int


@dataclass(frozen=True)
class Literal(Expr):
    value: Any


ComparisonOp = T_Literal["=", "<>", "<", "<=", ">", ">="]


@dataclass(frozen=True)
class Comparison(Expr):
    op: ComparisonOp
    left: Expr
    right: Expr


@dataclass(frozen=True)
class And(Expr):
    left: Expr
    right: Expr


@dataclass(frozen=True)
class Or(Expr):
    left: Expr
    right: Expr


@dataclass(frozen=True)
class Not(Expr):
    operand: Expr


@dataclass(frozen=True)
class IsNull(Expr):
    operand: Expr


@dataclass(frozen=True)
class IsNotNull(Expr):
    operand: Expr


def col(name: str) -> ColRef:
    return ColRef(name)


def param(index: int) -> Param:
    if index < 0:
        raise ValueError("parameter index must be non-negative")
    return Param(index)


def lit(value: Any) -> Literal:
    return Literal(value)


def eq(left: Expr, right: Expr) -> Comparison:
    return Comparison("=", left, right)


def ne(left: Expr, right: Expr) -> Comparison:
    return Comparison("<>", left, right)


def lt(left: Expr, right: Expr) -> Comparison:
    return Comparison("<", left, right)


def le(left: Expr, right: Expr) -> Comparison:
    return Comparison("<=", left, right)


def gt(left: Expr, right: Expr) -> Comparison:
    return Comparison(">", left, right)


def ge(left: Expr, right: Expr) -> Comparison:
    return Comparison(">=", left, right)


def and_(left: Expr, right: Expr, *rest: Expr) -> Expr:
    result = And(left, right)
    for item in rest:
        result = And(result, item)
    return result


def or_(left: Expr, right: Expr, *rest: Expr) -> Expr:
    result = Or(left, right)
    for item in rest:
        result = Or(result, item)
    return result


def not_(operand: Expr) -> Not:
    return Not(operand)


def is_null(operand: Expr) -> IsNull:
    return IsNull(operand)


def is_not_null(operand: Expr) -> IsNotNull:
    return IsNotNull(operand)


def expr_key(expr: Expr) -> tuple[Any, ...]:
    """Return a stable, parameter-value-independent expression identity."""
    if isinstance(expr, ColRef):
        return ("col", expr.name)
    if isinstance(expr, Param):
        return ("param", expr.index)
    if isinstance(expr, Literal):
        return ("literal", type(expr.value).__name__, expr.value)
    if isinstance(expr, Comparison):
        return ("cmp", expr.op, expr_key(expr.left), expr_key(expr.right))
    if isinstance(expr, And):
        return ("and", expr_key(expr.left), expr_key(expr.right))
    if isinstance(expr, Or):
        return ("or", expr_key(expr.left), expr_key(expr.right))
    if isinstance(expr, Not):
        return ("not", expr_key(expr.operand))
    if isinstance(expr, IsNull):
        return ("is_null", expr_key(expr.operand))
    if isinstance(expr, IsNotNull):
        return ("is_not_null", expr_key(expr.operand))
    raise TypeError(f"unsupported expression: {type(expr)!r}")


def walk_exprs(expr: Expr):
    yield expr
    if isinstance(expr, Comparison):
        yield from walk_exprs(expr.left)
        yield from walk_exprs(expr.right)
    elif isinstance(expr, (And, Or)):
        yield from walk_exprs(expr.left)
        yield from walk_exprs(expr.right)
    elif isinstance(expr, (Not, IsNull, IsNotNull)):
        yield from walk_exprs(expr.operand)
