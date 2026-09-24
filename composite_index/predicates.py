"""Typed predicate tree with SQL three-valued logic (TRUE/FALSE/UNKNOWN).

Evaluation keeps only rows whose predicate evaluates to TRUE, matching SQL
WHERE semantics.  ``nnf`` pushes NOT down to the leaves; every rewrite is
exact under three-valued logic (UNKNOWN stays UNKNOWN), so compilation and
the independent interpreter observe identical semantics.
"""
from __future__ import annotations

from dataclasses import dataclass

from .core import NULL


class Pred:
    __slots__ = ()


@dataclass(frozen=True)
class Cmp(Pred):
    col: str
    op: str  # lt le eq ge gt ne
    value: object


@dataclass(frozen=True)
class In(Pred):
    col: str
    values: tuple


@dataclass(frozen=True)
class NotIn(Pred):
    col: str
    values: tuple


@dataclass(frozen=True)
class IsNull(Pred):
    col: str


@dataclass(frozen=True)
class NotNull(Pred):
    col: str


@dataclass(frozen=True)
class And(Pred):
    children: tuple


@dataclass(frozen=True)
class Or(Pred):
    children: tuple


@dataclass(frozen=True)
class Not(Pred):
    child: Pred


_FLIP = {"lt": "ge", "le": "gt", "gt": "le", "ge": "lt", "eq": "ne", "ne": "eq"}


def _flatten(node):
    out = []
    for c in node.children:
        if isinstance(c, type(node)):
            out.extend(c.children)
        else:
            out.append(c)
    return type(node)(tuple(out))


def nnf(p: Pred) -> Pred:
    """Rewrite to negation normal form, flattening AND/OR."""
    if isinstance(p, Not):
        c = p.child
        if isinstance(c, Not):
            return nnf(c.child)
        if isinstance(c, And):
            return nnf(Or(tuple(Not(x) for x in c.children)))
        if isinstance(c, Or):
            return nnf(And(tuple(Not(x) for x in c.children)))
        if isinstance(c, Cmp):
            return Cmp(c.col, _FLIP[c.op], c.value)
        if isinstance(c, In):
            return NotIn(c.col, c.values)
        if isinstance(c, NotIn):
            return In(c.col, c.values)
        if isinstance(c, IsNull):
            return NotNull(c.col)
        if isinstance(c, NotNull):
            return IsNull(c.col)
        raise TypeError(c)
    if isinstance(p, And):
        return _flatten(And(tuple(nnf(x) for x in p.children)))
    if isinstance(p, Or):
        return _flatten(Or(tuple(nnf(x) for x in p.children)))
    return p


def _eq3(x, v):
    if x is NULL or v is NULL:
        return None
    return x == v


def eval3(p: Pred, row: dict):
    """Three-valued evaluation: returns True, False or None (UNKNOWN)."""
    if isinstance(p, Cmp):
        x = row.get(p.col, NULL)
        if x is NULL or p.value is NULL:
            return None
        if p.op == "lt":
            return x < p.value
        if p.op == "le":
            return x <= p.value
        if p.op == "eq":
            return x == p.value
        if p.op == "ge":
            return x >= p.value
        if p.op == "gt":
            return x > p.value
        if p.op == "ne":
            return x != p.value
        raise ValueError(p.op)
    if isinstance(p, (In, NotIn)):
        x = row.get(p.col, NULL)
        saw_unknown = False
        result = False
        for v in p.values:
            r = _eq3(x, v)
            if r is True:
                result = True
                break
            if r is None:
                saw_unknown = True
        else:
            result = None if saw_unknown else False
        if isinstance(p, NotIn):
            return None if result is None else not result
        return result
    if isinstance(p, IsNull):
        return row.get(p.col, NULL) is NULL
    if isinstance(p, NotNull):
        return row.get(p.col, NULL) is not NULL
    if isinstance(p, And):
        result = True
        for c in p.children:
            v = eval3(c, row)
            if v is False:
                return False
            if v is None:
                result = None
        return result
    if isinstance(p, Or):
        result = False
        for c in p.children:
            v = eval3(c, row)
            if v is True:
                return True
            if v is None:
                result = None
        return result
    if isinstance(p, Not):
        v = eval3(p.child, row)
        return None if v is None else not v
    raise TypeError(p)


def _ser_val(v):
    if v is NULL:
        return {"$": "null"}
    return v


def to_dict(p: Pred):
    """Canonical, JSON-safe encoding used for fingerprints and reports."""
    if isinstance(p, Cmp):
        return {"cmp": [p.col, p.op, _ser_val(p.value)]}
    if isinstance(p, In):
        return {"in": [p.col, [_ser_val(v) for v in p.values]]}
    if isinstance(p, NotIn):
        return {"not_in": [p.col, [_ser_val(v) for v in p.values]]}
    if isinstance(p, IsNull):
        return {"is_null": p.col}
    if isinstance(p, NotNull):
        return {"not_null": p.col}
    if isinstance(p, And):
        return {"and": [to_dict(c) for c in p.children]}
    if isinstance(p, Or):
        return {"or": [to_dict(c) for c in p.children]}
    if isinstance(p, Not):
        return {"not": to_dict(p.child)}
    raise TypeError(p)
