"""Typed predicate trees with SQL three-valued (Kleene) semantics.

Evaluation returns True / False / None(UNKNOWN).  A row qualifies for a query
only when the predicate evaluates to True.
"""
from __future__ import annotations

from dataclasses import dataclass

OPS = ("lt", "le", "eq", "ge", "gt", "ne")


@dataclass(frozen=True)
class Param:
    """A query parameter placeholder, resolved from the params dict."""
    name: str


@dataclass(frozen=True)
class Cmp:
    col: int
    op: str  # one of OPS
    value: object  # literal or Param

    def __post_init__(self):
        if self.op not in OPS:
            raise ValueError(f"bad comparison op: {self.op!r}")


@dataclass(frozen=True)
class In:
    col: int
    values: tuple

    def __init__(self, col, values):
        object.__setattr__(self, "col", col)
        object.__setattr__(self, "values", tuple(values))


@dataclass(frozen=True)
class IsNull:
    col: int


@dataclass(frozen=True)
class And:
    children: tuple

    def __init__(self, *children):
        object.__setattr__(self, "children", tuple(children))


@dataclass(frozen=True)
class Or:
    children: tuple

    def __init__(self, *children):
        object.__setattr__(self, "children", tuple(children))


@dataclass(frozen=True)
class Not:
    child: object


def resolve(v, params):
    if isinstance(v, Param):
        return params[v.name]
    return v


def kleene_not(v):
    return None if v is None else (not v)


def eval_pred(pred, row, params=None):
    """Three-valued evaluation; True means the row qualifies."""
    return eval_count(pred, row, params or {})[0]


def eval_count(pred, row, params):
    """Like eval_pred but also returns the number of atomic comparisons
    actually evaluated (short-circuiting AND/OR).  Used for cost accounting."""
    if isinstance(pred, Cmp):
        v = row[pred.col]
        w = resolve(pred.value, params)
        if v is None or w is None:
            return None, 1
        op = pred.op
        if op == "lt":
            r = v < w
        elif op == "le":
            r = v <= w
        elif op == "eq":
            r = v == w
        elif op == "ge":
            r = v >= w
        elif op == "gt":
            r = v > w
        else:  # ne
            r = v != w
        return (True if r else False), 1
    if isinstance(pred, In):
        v = row[pred.col]
        vals = [resolve(x, params) for x in pred.values]
        if v is None:
            return None, 1
        if any(x is not None and x == v for x in vals):
            return True, 1
        if any(x is None for x in vals):
            return None, 1
        return False, 1
    if isinstance(pred, IsNull):
        return (True if row[pred.col] is None else False), 1
    if isinstance(pred, NotIn):
        r, n = eval_count(In(pred.col, pred.values), row, params)
        return kleene_not(r), n
    if isinstance(pred, IsNotNull):
        return (False if row[pred.col] is None else True), 1
    if isinstance(pred, Not):
        r, n = eval_count(pred.child, row, params)
        return kleene_not(r), n
    if isinstance(pred, And):
        total = 0
        unknown = False
        for c in pred.children:
            r, n = eval_count(c, row, params)
            total += n
            if r is False:
                return False, total
            if r is None:
                unknown = True
        return (None if unknown else True), total
    if isinstance(pred, Or):
        total = 0
        unknown = False
        for c in pred.children:
            r, n = eval_count(c, row, params)
            total += n
            if r is True:
                return True, total
            if r is None:
                unknown = True
        return (None if unknown else False), total
    raise TypeError(f"unknown predicate node: {pred!r}")


def atom_count(pred):
    """Number of atomic (Cmp/In/IsNull) nodes; used as compare-cost factor."""
    if isinstance(pred, (Cmp, In, IsNull, NotIn, IsNotNull)):
        return 1
    if isinstance(pred, Not):
        return atom_count(pred.child)
    return sum(atom_count(c) for c in pred.children)


_NEG_OP = {"lt": "ge", "le": "gt", "gt": "le", "ge": "lt", "eq": "ne", "ne": "eq"}


@dataclass(frozen=True)
class NotIn:
    """Internal node produced by negate(); users normally write Not(In(...))."""
    col: int
    values: tuple

    def __init__(self, col, values):
        object.__setattr__(self, "col", col)
        object.__setattr__(self, "values", tuple(values))


@dataclass(frozen=True)
class IsNotNull:
    """Internal node produced by negate(); users normally write Not(IsNull())."""
    col: int


def negate(pred):
    """Push a NOT down to the atoms (negation normal form).

    Valid under Kleene three-valued logic: NOT distributes over AND/OR and
    NOT UNKNOWN = UNKNOWN, so e.g. NOT (a < 5) is exactly a >= 5 including its
    UNKNOWN behaviour.  Keeping negations atomic lets the range compiler build
    *covers* for negated predicates directly; complementing an already
    widened multi-column interval set would silently drop qualifying rows.
    """
    if isinstance(pred, Not):
        return pred.child
    if isinstance(pred, And):
        return Or(*(negate(c) for c in pred.children))
    if isinstance(pred, Or):
        return And(*(negate(c) for c in pred.children))
    if isinstance(pred, Cmp):
        return Cmp(pred.col, _NEG_OP[pred.op], pred.value)
    if isinstance(pred, In):
        return NotIn(pred.col, pred.values)
    if isinstance(pred, NotIn):
        return In(pred.col, pred.values)
    if isinstance(pred, IsNull):
        return IsNotNull(pred.col)
    if isinstance(pred, IsNotNull):
        return IsNull(pred.col)
    raise TypeError(f"unknown predicate node: {pred!r}")
