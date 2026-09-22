"""Bag (multiset) relational operators over lists of dict rows.

Multiplicity contract:
  * relations are Python lists; duplicate rows are significant;
  * selection keeps every qualifying occurrence;
  * inner join emits one row per matching *pair* of occurrences, so
    multiplicities multiply;
  * left outer join additionally emits each unmatched left occurrence
    exactly once, padded with NULL on every right-side attribute.
"""

from __future__ import annotations

from typing import Any, Iterable

from .expr import Row, eval_pred
from .tvl import NULL

Relation = list[Row]


def select(rel: Relation, pred: Any) -> Relation:
    """sigma_pred(rel): keep rows where pred is TRUE (UNKNOWN/FALSE dropped)."""
    return [row for row in rel if eval_pred(pred, row).is_true()]


def inner_join(left: Relation, right: Relation, theta: Any) -> Relation:
    """R |x|_theta S under bag semantics: one output per matching pair."""
    out: Relation = []
    for lrow in left:
        for rrow in right:
            merged = {**lrow, **rrow}
            if eval_pred(theta, merged).is_true():
                out.append(merged)
    return out


def left_join(
    left: Relation,
    right: Relation,
    theta: Any,
    right_attrs: Iterable[str],
) -> Relation:
    """R ⟕_theta S: inner-join matches plus NULL-padded unmatched left rows."""
    right_attrs = tuple(right_attrs)
    out: Relation = []
    for lrow in left:
        matched = False
        for rrow in right:
            merged = {**lrow, **rrow}
            if eval_pred(theta, merged).is_true():
                out.append(merged)
                matched = True
        if not matched:
            out.append({**lrow, **{attr: NULL for attr in right_attrs}})
    return out


def bag_eq(rel_a: Relation, rel_b: Relation) -> bool:
    """Multiset equality: same rows with the same multiplicities, any order."""
    remaining = list(rel_b)
    for row in rel_a:
        try:
            remaining.remove(row)
        except ValueError:
            return False
    return not remaining
