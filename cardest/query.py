"""Query predicates: equality / range filters with AND / OR / NOT.

SQL three-valued logic: every predicate evaluates to True, False or
UNKNOWN (None). A WHERE clause keeps a row only when it evaluates to True.
"""
from __future__ import annotations

from .spec import SpecError

UNKNOWN = None


def sql_and(values):
    if any(v is False for v in values):
        return False
    if any(v is None for v in values):
        return UNKNOWN
    return True


def sql_or(values):
    if any(v is True for v in values):
        return True
    if any(v is None for v in values):
        return UNKNOWN
    return False


def sql_not(value):
    if value is None:
        return UNKNOWN
    return not value


def validate_predicate(pred, valid_columns):
    """valid_columns: set of column references allowed (e.g. {'a','b'} for a
    select query, {'R.a','S.x',...} for a join query)."""
    if not isinstance(pred, dict):
        raise SpecError("predicate must be an object")
    op = pred.get("op")
    if op in ("and", "or"):
        args = pred.get("args")
        if not isinstance(args, list) or not args:
            raise SpecError(f"{op}: 'args' must be a non-empty list")
        for a in args:
            validate_predicate(a, valid_columns)
    elif op == "not":
        validate_predicate(pred.get("arg"), valid_columns)
    elif op == "eq":
        _check_column(pred, valid_columns)
        val = pred.get("value")
        if val is not None and (not isinstance(val, int) or isinstance(val, bool)):
            raise SpecError("eq: value must be an int or null")
    elif op == "range":
        _check_column(pred, valid_columns)
        for key in ("low", "high"):
            val = pred.get(key)
            if val is not None and (not isinstance(val, int) or isinstance(val, bool)):
                raise SpecError(f"range: {key} must be an int or null")
    else:
        raise SpecError(f"unknown predicate op {op!r}")


def _check_column(pred, valid_columns):
    col = pred.get("column")
    if col not in valid_columns:
        raise SpecError(f"predicate references unknown column {col!r}")


def eval_predicate(pred, getter):
    """Evaluate a predicate; getter(column_name) -> int or None (NULL)."""
    op = pred["op"]
    if op == "and":
        return sql_and([eval_predicate(a, getter) for a in pred["args"]])
    if op == "or":
        return sql_or([eval_predicate(a, getter) for a in pred["args"]])
    if op == "not":
        return sql_not(eval_predicate(pred["arg"], getter))
    if op == "eq":
        value = getter(pred["column"])
        target = pred["value"]
        if value is None or target is None:
            return UNKNOWN
        return value == target
    if op == "range":
        value = getter(pred["column"])
        if value is None:
            return UNKNOWN
        low = pred.get("low")
        high = pred.get("high")
        if low is not None and value < low:
            return False
        if high is not None and value > high:
            return False
        return True
    raise SpecError(f"unknown predicate op {op!r}")


def query_valid_columns(spec):
    """Column references allowed in the query's WHERE clause."""
    query = spec["query"]
    if query["type"] == "select":
        rel = query["from"]
        return set(spec["relations"][rel]["columns"].keys())
    cols = set()
    for side in ("left", "right"):
        rel = query[side]
        for c in spec["relations"][rel]["columns"]:
            cols.add(f"{rel}.{c}")
    return cols


def validate_query_predicate(spec):
    where = spec["query"].get("where")
    if where is not None:
        validate_predicate(where, query_valid_columns(spec))
