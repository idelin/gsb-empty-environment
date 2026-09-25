"""Point estimates under a column-independence assumption.

These numbers are *heuristics*, computed from the marginals alone.  They are
NOT mathematical bounds and are always labelled as such; no confidence
interval produced here should ever be read as a certified interval.
"""
from __future__ import annotations

from .model import Join, Select


def _selectivity(pred, table):
    rows = table.row_count
    op = pred[0]
    if op == "true":
        return 1.0
    if rows <= 0:
        return 0.0
    if op == "eq":
        _, col, val = pred
        if val is None:
            return 0.0
        c = table.column(col)
        return c.histogram.get(val, 0) / rows
    if op == "range":
        _, col, lo, hi = pred
        if lo is None or hi is None:
            return 0.0
        c = table.column(col)
        return sum(c.histogram.get(v, 0) for v in c.domain if lo <= v <= hi) / rows
    if op == "and":
        s = 1.0
        for sub in pred[1]:
            s *= _selectivity(sub, table)
        return s
    if op == "or":
        s = 1.0
        for sub in pred[1]:
            s *= 1.0 - _selectivity(sub, table)
        return 1.0 - s
    if op == "not":
        return 1.0 - _selectivity(pred[1], table)
    raise ValueError(f"unknown predicate operator {op!r}")


def independence_estimate(tables, query):
    """Returns a labelled point estimate (never a bound)."""
    if isinstance(query, Select):
        table = tables[query.table]
        value = table.row_count * _selectivity(query.where, table)
    elif isinstance(query, Join):
        left = tables[query.left_table]
        right = tables[query.right_table]
        lk = left.column(query.left_key)
        rk = right.column(query.right_key)
        sl = _selectivity(query.left_where, left)
        sr = _selectivity(query.right_where, right)
        value = 0.0
        for key in set(lk.domain) & set(rk.domain):
            value += (lk.histogram.get(key, 0) * sl) * (rk.histogram.get(key, 0) * sr)
    else:  # pragma: no cover - defensive
        raise TypeError(query)
    return {
        "method": "column-independence assumption",
        "value": value,
        "is_bound": False,
        "note": (
            "heuristic point estimate only; NOT a mathematical bound, and no "
            "empirical confidence interval should be treated as one"
        ),
    }
