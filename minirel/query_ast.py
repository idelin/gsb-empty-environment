"""Query AST for the supported subset.

Rows are tuples; fields are ints or None (null).  A Query has:
  - an outer table name,
  - projection expressions (unknown boolean values stay None in output),
  - an optional WHERE expression (only TRUE keeps the row).

Subquery predicates (InPred / ExistsPred) may appear in both WHERE and
projection.  A Subquery carries its inner table, the select expressions
compared by IN, correlation conditions (regular or null-safe equality on
outer/inner expression pairs) and an inner-only filter.
"""

from __future__ import annotations

from dataclasses import dataclass

from .tvl import eq3, eq_nullsafe, tvl_and, tvl_not, tvl_or


class Expr:
    __slots__ = ()


@dataclass(frozen=True)
class Col(Expr):
    idx: int


@dataclass(frozen=True)
class Lit(Expr):
    val: object


@dataclass(frozen=True)
class Eq(Expr):
    left: Expr
    right: Expr
    null_safe: bool = False


@dataclass(frozen=True)
class And(Expr):
    args: tuple


@dataclass(frozen=True)
class Or(Expr):
    args: tuple


@dataclass(frozen=True)
class Not(Expr):
    arg: Expr


@dataclass(frozen=True)
class CorrCond:
    outer: Expr
    inner: Expr
    null_safe: bool = False


@dataclass(frozen=True)
class Subquery:
    table: str
    select: tuple = ()
    corr: tuple = ()
    filter: Expr | None = None


@dataclass(frozen=True)
class InPred(Expr):
    outer: tuple
    sub: Subquery
    negated: bool = False


@dataclass(frozen=True)
class ExistsPred(Expr):
    sub: Subquery
    negated: bool = False


@dataclass(frozen=True)
class Query:
    outer: str
    select: tuple
    where: Expr | None = None


def eval_expr(e, row):
    """Evaluate a subquery-free expression against a row."""
    if isinstance(e, Col):
        return row[e.idx]
    if isinstance(e, Lit):
        return e.val
    if isinstance(e, Eq):
        left = eval_expr(e.left, row)
        right = eval_expr(e.right, row)
        return eq_nullsafe(left, right) if e.null_safe else eq3(left, right)
    if isinstance(e, And):
        result = True
        for arg in e.args:
            result = tvl_and(result, eval_expr(arg, row))
        return result
    if isinstance(e, Or):
        result = False
        for arg in e.args:
            result = tvl_or(result, eval_expr(arg, row))
        return result
    if isinstance(e, Not):
        return tvl_not(eval_expr(e.arg, row))
    raise TypeError(f"not a plain expression: {e!r}")


def eval_scalar(e, row, pred_eval):
    """Evaluate any scalar expression; subquery predicates go to pred_eval."""
    if isinstance(e, (InPred, ExistsPred)):
        return pred_eval(e, row)
    if isinstance(e, And):
        result = True
        for arg in e.args:
            result = tvl_and(result, eval_scalar(arg, row, pred_eval))
        return result
    if isinstance(e, Or):
        result = False
        for arg in e.args:
            result = tvl_or(result, eval_scalar(arg, row, pred_eval))
        return result
    if isinstance(e, Not):
        return tvl_not(eval_scalar(e.arg, row, pred_eval))
    return eval_expr(e, row)


def find_subquery_preds(query):
    """Distinct subquery predicates used by a query (WHERE + projection)."""
    seen = {}

    def walk(e):
        if isinstance(e, (InPred, ExistsPred)):
            seen.setdefault(e.sub, e)
        elif isinstance(e, (And, Or)):
            for arg in e.args:
                walk(arg)
        elif isinstance(e, Not):
            walk(e.arg)
        elif isinstance(e, Eq):
            walk(e.left)
            walk(e.right)

    for e in query.select:
        walk(e)
    if query.where is not None:
        walk(query.where)
    return list(seen.values())
