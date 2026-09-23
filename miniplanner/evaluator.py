"""Three-valued predicate evaluation and post-binding simplification."""

from __future__ import annotations

from typing import Any, Mapping, Sequence

from miniplanner.catalog import ColumnStats, Table, comparable_types, logical_type, sql_compare
from miniplanner.expr import (
    And,
    ColRef,
    Comparison,
    Expr,
    IsNotNull,
    IsNull,
    Literal,
    Not,
    Or,
    Param,
)

TRUE = Literal(True)
FALSE = Literal(False)
UNKNOWN = Literal(None)


def eval_scalar(expr: Expr, row: Mapping[str, Any], params: Sequence[Any]) -> Any:
    if isinstance(expr, ColRef):
        return row.get(expr.name)
    if isinstance(expr, Param):
        return params[expr.index]
    if isinstance(expr, Literal):
        return expr.value
    raise TypeError(f"unsupported scalar expression: {type(expr)!r}")


def truth_value(value: Any) -> bool | None:
    if value is None:
        return None
    return bool(value)


def eval_predicate(expr: Expr, row: Mapping[str, Any], params: Sequence[Any]) -> bool | None:
    if isinstance(expr, Literal):
        return truth_value(expr.value)
    if isinstance(expr, Comparison):
        left = eval_scalar(expr.left, row, params)
        right = eval_scalar(expr.right, row, params)
        return sql_compare(left, right, expr.op)
    if isinstance(expr, IsNull):
        return eval_scalar(expr.operand, row, params) is None
    if isinstance(expr, IsNotNull):
        return eval_scalar(expr.operand, row, params) is not None
    if isinstance(expr, Not):
        value = eval_predicate(expr.operand, row, params)
        return None if value is None else not value
    if isinstance(expr, And):
        left = eval_predicate(expr.left, row, params)
        right = eval_predicate(expr.right, row, params)
        if left is False or right is False:
            return False
        if left is True:
            return right
        if right is True:
            return left
        return None
    if isinstance(expr, Or):
        left = eval_predicate(expr.left, row, params)
        right = eval_predicate(expr.right, row, params)
        if left is True or right is True:
            return True
        if left is False:
            return right
        if right is False:
            return left
        return None
    raise TypeError(f"unsupported predicate: {type(expr)!r}")


def is_true(value: bool | None) -> bool:
    return value is True


def _column_comparison(expr: Comparison) -> tuple[str, ComparisonOp, Expr] | None:
    if isinstance(expr.left, ColRef) and not isinstance(expr.right, ColRef):
        return expr.left.name, expr.op, expr.right
    if isinstance(expr.right, ColRef) and not isinstance(expr.left, ColRef):
        flipped = {"=": "=", "<>": "<>", "<": ">", "<=": ">=", ">": "<", ">=": "<="}
        return expr.right.name, flipped[expr.op], expr.left
    return None


def _comparison_static_result(op: str, value: Any, stats: ColumnStats) -> bool | None:
    if value is None:
        return None
    if stats.value_type == "mixed":
        return None
    if not comparable_types(stats.value_type, logical_type(value)):
        return None
    minimum = stats.min_value
    maximum = stats.max_value
    if minimum is None or maximum is None:
        return None
    if op == "=":
        if value < minimum or value > maximum:
            return False
    elif op == "<":
        if minimum >= value:
            return False
    elif op == "<=":
        if minimum > value:
            return False
    elif op == ">":
        if maximum <= value:
            return False
    elif op == ">=":
        if maximum < value:
            return False
    return None


def bind_and_simplify(
    expr: Expr,
    table: Table,
    params: Sequence[Any],
) -> Expr:
    """Simplify a predicate for one execution after parameter binding.

    The simplifier folds only facts that are independent of a row: null
    comparisons, non-comparable type families, and stats-proven contradictions.
    Ordinary in-domain values remain comparisons so the current parameter is read
    during execution.
    """
    if isinstance(expr, Literal):
        return expr
    if isinstance(expr, Comparison):
        column_match = _column_comparison(expr)
        if column_match is not None:
            column, op, scalar = column_match
            value = eval_scalar(scalar, {}, params)
            static_result = _comparison_static_result(op, value, table.column_stats(column))
            if static_result is None and value is None:
                return UNKNOWN
            if static_result is not None:
                return TRUE if static_result else FALSE
            return expr
        left = eval_scalar(expr.left, {}, params)
        right = eval_scalar(expr.right, {}, params)
        return Literal(sql_compare(left, right, expr.op))
    if isinstance(expr, IsNull):
        if isinstance(expr.operand, (Param, Literal)):
            return Literal(eval_scalar(expr.operand, {}, params) is None)
        return expr
    if isinstance(expr, IsNotNull):
        if isinstance(expr.operand, (Param, Literal)):
            return Literal(eval_scalar(expr.operand, {}, params) is not None)
        return expr
    if isinstance(expr, Not):
        operand = bind_and_simplify(expr.operand, table, params)
        if isinstance(operand, Literal):
            return Literal(None if operand.value is None else not operand.value)
        return Not(operand)
    if isinstance(expr, And):
        left = bind_and_simplify(expr.left, table, params)
        right = bind_and_simplify(expr.right, table, params)
        if isinstance(left, Literal):
            if left.value is False:
                return FALSE
            if left.value is True:
                return right
        if isinstance(right, Literal):
            if right.value is False:
                return FALSE
            if right.value is True:
                return left
        if isinstance(left, Literal) and isinstance(right, Literal):
            return UNKNOWN
        return And(left, right)
    if isinstance(expr, Or):
        left = bind_and_simplify(expr.left, table, params)
        right = bind_and_simplify(expr.right, table, params)
        if isinstance(left, Literal):
            if left.value is True:
                return TRUE
            if left.value is False:
                return right
        if isinstance(right, Literal):
            if right.value is True:
                return TRUE
            if right.value is False:
                return left
        if isinstance(left, Literal) and isinstance(right, Literal):
            return UNKNOWN
        return Or(left, right)
    raise TypeError(f"unsupported predicate: {type(expr)!r}")


def flatten_and(expr: Expr) -> list[Expr]:
    if isinstance(expr, And):
        return flatten_and(expr.left) + flatten_and(expr.right)
    return [expr]
