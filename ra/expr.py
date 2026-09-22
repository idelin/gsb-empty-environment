"""Predicate AST, evaluation under NULL semantics, and null-rejection analysis.

Null-rejection (textbook definition, made executable):
  A predicate p is *null-rejecting* on an attribute set A iff, whenever every
  attribute in A is NULL, p cannot evaluate to TRUE -- no matter what values
  (including NULL) the remaining attributes take.

The analysis is a sound, syntax-directed approximation built from two
mutually recursive predicates computed under the assumption "all of A is NULL":

  never_true(p)  -- p cannot be TRUE      (this *is* null-rejection)
  never_false(p) -- p cannot be FALSE

plus, for value expressions:
  may_null(e)    -- e might be NULL       (any stored column may contain NULL)
  must_null(e)   -- e is definitely NULL  (columns of A, or the literal NULL)
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Callable

from .tvl import NULL, Tri

Row = dict[str, Any]

# ---------------------------------------------------------------------------
# Value expressions
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class Col:
    name: str


@dataclass(frozen=True)
class Lit:
    value: Any  # may be NULL


# ---------------------------------------------------------------------------
# Predicates
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class Cmp:
    op: str  # one of = <> < <= > >=
    left: Any
    right: Any


@dataclass(frozen=True)
class IsNull:
    operand: Any


@dataclass(frozen=True)
class IsNotNull:
    operand: Any


@dataclass(frozen=True)
class And:
    left: Any
    right: Any


@dataclass(frozen=True)
class Or:
    left: Any
    right: Any


@dataclass(frozen=True)
class Not:
    operand: Any


_OPS: dict[str, Callable[[Any, Any], bool]] = {
    "=": lambda a, b: a == b,
    "<>": lambda a, b: a != b,
    "<": lambda a, b: a < b,
    "<=": lambda a, b: a <= b,
    ">": lambda a, b: a > b,
    ">=": lambda a, b: a >= b,
}


# ---------------------------------------------------------------------------
# Evaluation
# ---------------------------------------------------------------------------


def eval_value(expr: Any, row: Row) -> Any:
    """Evaluate a value expression; result may be NULL."""
    if isinstance(expr, Col):
        return row[expr.name]
    if isinstance(expr, Lit):
        return expr.value
    raise TypeError(f"not a value expression: {expr!r}")


def eval_pred(pred: Any, row: Row) -> Tri:
    """Evaluate a predicate to a three-valued truth."""
    if isinstance(pred, Cmp):
        left = eval_value(pred.left, row)
        right = eval_value(pred.right, row)
        if left is NULL or right is NULL:
            return Tri.UNKNOWN
        return Tri.TRUE if _OPS[pred.op](left, right) else Tri.FALSE
    if isinstance(pred, IsNull):
        return Tri.TRUE if eval_value(pred.operand, row) is NULL else Tri.FALSE
    if isinstance(pred, IsNotNull):
        return Tri.FALSE if eval_value(pred.operand, row) is NULL else Tri.TRUE
    if isinstance(pred, And):
        return eval_pred(pred.left, row) & eval_pred(pred.right, row)
    if isinstance(pred, Or):
        return eval_pred(pred.left, row) | eval_pred(pred.right, row)
    if isinstance(pred, Not):
        return ~eval_pred(pred.operand, row)
    if isinstance(pred, Lit) and isinstance(pred.value, bool):
        return Tri.TRUE if pred.value else Tri.FALSE
    raise TypeError(f"not a predicate: {pred!r}")


def attrs(expr: Any) -> frozenset[str]:
    """Column names referenced by an expression."""
    if isinstance(expr, Col):
        return frozenset({expr.name})
    if isinstance(expr, Lit):
        return frozenset()
    if isinstance(expr, Cmp):
        return attrs(expr.left) | attrs(expr.right)
    if isinstance(expr, (IsNull, IsNotNull, Not)):
        return attrs(expr.operand)
    if isinstance(expr, (And, Or)):
        return attrs(expr.left) | attrs(expr.right)
    raise TypeError(f"unknown expression: {expr!r}")


# ---------------------------------------------------------------------------
# Null-rejection analysis
# ---------------------------------------------------------------------------


def may_null(expr: Any, nulled: frozenset[str]) -> bool:
    """Could `expr` be NULL, assuming all columns in `nulled` are NULL?

    Any stored column may contain NULL in the data, so only literals are
    definitely non-NULL.
    """
    if isinstance(expr, Col):
        return True
    if isinstance(expr, Lit):
        return expr.value is NULL
    raise TypeError(f"not a value expression: {expr!r}")


def must_null(expr: Any, nulled: frozenset[str]) -> bool:
    """Is `expr` definitely NULL, assuming all columns in `nulled` are NULL?"""
    if isinstance(expr, Col):
        return expr.name in nulled
    if isinstance(expr, Lit):
        return expr.value is NULL
    raise TypeError(f"not a value expression: {expr!r}")


def never_true(pred: Any, nulled: frozenset[str]) -> bool:
    """Can `pred` never be TRUE when all columns in `nulled` are NULL?"""
    if isinstance(pred, Cmp):
        # Any definitely-NULL operand forces UNKNOWN.
        return must_null(pred.left, nulled) or must_null(pred.right, nulled)
    if isinstance(pred, IsNull):
        # TRUE exactly when the operand is NULL; never TRUE if it cannot be.
        return not may_null(pred.operand, nulled)
    if isinstance(pred, IsNotNull):
        return must_null(pred.operand, nulled)
    if isinstance(pred, And):
        # FALSE/UNKNOWN dominates: one non-TRUE conjunct suffices.
        return never_true(pred.left, nulled) or never_true(pred.right, nulled)
    if isinstance(pred, Or):
        # TRUE leaks through unless both disjuncts are non-TRUE.
        return never_true(pred.left, nulled) and never_true(pred.right, nulled)
    if isinstance(pred, Not):
        return never_false(pred.operand, nulled)
    if isinstance(pred, Lit) and isinstance(pred.value, bool):
        return not pred.value
    return False  # conservative fallback


def never_false(pred: Any, nulled: frozenset[str]) -> bool:
    """Can `pred` never be FALSE when all columns in `nulled` are NULL?"""
    if isinstance(pred, Cmp):
        return must_null(pred.left, nulled) or must_null(pred.right, nulled)
    if isinstance(pred, IsNull):
        return must_null(pred.operand, nulled)
    if isinstance(pred, IsNotNull):
        return not may_null(pred.operand, nulled)
    if isinstance(pred, And):
        return never_false(pred.left, nulled) and never_false(pred.right, nulled)
    if isinstance(pred, Or):
        return never_false(pred.left, nulled) or never_false(pred.right, nulled)
    if isinstance(pred, Not):
        return never_true(pred.operand, nulled)
    if isinstance(pred, Lit) and isinstance(pred.value, bool):
        return pred.value
    return False  # conservative fallback


def is_null_rejecting(pred: Any, null_attrs: frozenset[str] | set[str]) -> bool:
    """True iff `pred` rejects (is non-TRUE on) rows where `null_attrs` are NULL."""
    return never_true(pred, frozenset(null_attrs))
