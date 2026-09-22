"""A tiny relational algebra engine with explicit SQL NULL and multiset semantics.

Design notes
------------
* ``NULL`` is a singleton sentinel. Any comparison involving NULL evaluates to
  ``UNKNOWN`` (SQL three-valued logic, 3VL).
* A ``Relation`` is a *multiset* of rows: duplicate rows are significant and
  preserved by projection. Row order is never significant.
* Selection keeps only rows whose predicate evaluates to True; UNKNOWN rows
  are rejected, exactly as in SQL WHERE.
* Joins are equijoins; NULL never matches NULL. Outer joins pad the
  non-matching side with NULLs.
* No SQL engine is involved; everything is evaluated directly in Python.

All attribute names used within a single plan must be globally unique
(this keeps row dictionaries unambiguous).
"""

from __future__ import annotations

from collections import Counter
from dataclasses import dataclass
from typing import Callable, Optional, Sequence, Union


class _NullType:
    """Singleton SQL NULL value."""

    _instance: Optional["_NullType"] = None

    def __new__(cls) -> "_NullType":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "NULL"

    def __copy__(self) -> "_NullType":
        return self

    def __deepcopy__(self, memo: dict) -> "_NullType":
        return self


NULL = _NullType()


class _UnknownType:
    """The third truth value of SQL three-valued logic."""

    _instance: Optional["_UnknownType"] = None

    def __new__(cls) -> "_UnknownType":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "UNKNOWN"

    def __bool__(self) -> bool:
        raise TypeError("UNKNOWN has no boolean value (SQL 3VL)")


UNKNOWN = _UnknownType()

TriState = Union[bool, _UnknownType]


# ---------------------------------------------------------------------------
# Three-valued logic
# ---------------------------------------------------------------------------

def tri_not(v: TriState) -> TriState:
    if v is UNKNOWN:
        return UNKNOWN
    return not v


def tri_and(a: TriState, b: TriState) -> TriState:
    if a is False or b is False:
        return False
    if a is UNKNOWN or b is UNKNOWN:
        return UNKNOWN
    return True


def tri_or(a: TriState, b: TriState) -> TriState:
    if a is True or b is True:
        return True
    if a is UNKNOWN or b is UNKNOWN:
        return UNKNOWN
    return False


def _sql_cmp(a, b, op) -> TriState:
    if a is NULL or b is NULL:
        return UNKNOWN
    return op(a, b)


def sql_eq(a, b) -> TriState:
    return _sql_cmp(a, b, lambda x, y: x == y)


def sql_ne(a, b) -> TriState:
    return _sql_cmp(a, b, lambda x, y: x != y)


def sql_lt(a, b) -> TriState:
    return _sql_cmp(a, b, lambda x, y: x < y)


def sql_le(a, b) -> TriState:
    return _sql_cmp(a, b, lambda x, y: x <= y)


def sql_gt(a, b) -> TriState:
    return _sql_cmp(a, b, lambda x, y: x > y)


def sql_ge(a, b) -> TriState:
    return _sql_cmp(a, b, lambda x, y: x >= y)


def sql_is_null(a) -> bool:
    return a is NULL


# ---------------------------------------------------------------------------
# Relations
# ---------------------------------------------------------------------------

Row = tuple


@dataclass(frozen=True)
class Relation:
    """A named-schema multiset of rows. Duplicates are significant."""

    schema: tuple[str, ...]
    rows: tuple[Row, ...]

    def multiset(self) -> Counter:
        return Counter(self.rows)


def same_multiset(a: Relation, b: Relation) -> bool:
    """Order-insensitive, duplicate-sensitive equality."""
    return a.schema == b.schema and a.multiset() == b.multiset()


# ---------------------------------------------------------------------------
# Predicates (operate on row dicts, return TriState)
# ---------------------------------------------------------------------------

Pred = Callable[[dict], TriState]
Value = Callable[[dict], object]


def col(name: str) -> Value:
    return lambda row: row[name]


def lit(value) -> Value:
    return lambda row: value


def _binop(f, left: Value, right: Value) -> Pred:
    return lambda row: f(left(row), right(row))


def eq(left: Value, right: Value) -> Pred:
    return _binop(sql_eq, left, right)


def ne(left: Value, right: Value) -> Pred:
    return _binop(sql_ne, left, right)


def lt(left: Value, right: Value) -> Pred:
    return _binop(sql_lt, left, right)


def le(left: Value, right: Value) -> Pred:
    return _binop(sql_le, left, right)


def gt(left: Value, right: Value) -> Pred:
    return _binop(sql_gt, left, right)


def ge(left: Value, right: Value) -> Pred:
    return _binop(sql_ge, left, right)


def and_(*preds: Pred) -> Pred:
    return lambda row: _fold(tri_and, (p(row) for p in preds))


def or_(*preds: Pred) -> Pred:
    return lambda row: _fold(tri_or, (p(row) for p in preds))


def not_(pred: Pred) -> Pred:
    return lambda row: tri_not(pred(row))


def is_null(value: Value) -> Pred:
    return lambda row: sql_is_null(value(row))


def _fold(op, values) -> TriState:
    acc: TriState = True if op is tri_and else False
    for v in values:
        acc = op(acc, v)
    return acc


# ---------------------------------------------------------------------------
# Query plans
# ---------------------------------------------------------------------------

class Plan:
    """Base class of the plan tree nodes."""


@dataclass(frozen=True)
class Scan(Plan):
    name: str


@dataclass(frozen=True)
class Select(Plan):
    pred: Pred
    child: Plan


@dataclass(frozen=True)
class Project(Plan):
    attrs: tuple[str, ...]
    child: Plan


INNER, LEFT, RIGHT, FULL = "inner", "left", "right", "full"


@dataclass(frozen=True)
class Join(Plan):
    left: Plan
    right: Plan
    on: tuple[tuple[str, str], ...]  # equality pairs (left_attr, right_attr)
    how: str = INNER

    def __post_init__(self):
        if self.how not in (INNER, LEFT, RIGHT, FULL):
            raise ValueError(f"unknown join kind: {self.how!r}")


# ---------------------------------------------------------------------------
# Evaluation
# ---------------------------------------------------------------------------

def evaluate(plan: Plan, db: dict[str, Relation]) -> Relation:
    if isinstance(plan, Scan):
        return db[plan.name]
    if isinstance(plan, Select):
        return _eval_select(plan, db)
    if isinstance(plan, Project):
        return _eval_project(plan, db)
    if isinstance(plan, Join):
        return _eval_join(plan, db)
    raise TypeError(f"unknown plan node: {plan!r}")


def _eval_select(plan: Select, db) -> Relation:
    child = evaluate(plan.child, db)
    kept = [
        row
        for row in child.rows
        if plan.pred(dict(zip(child.schema, row))) is True
    ]
    return Relation(child.schema, tuple(kept))


def _eval_project(plan: Project, db) -> Relation:
    child = evaluate(plan.child, db)
    idx = [child.schema.index(a) for a in plan.attrs]
    rows = tuple(tuple(row[i] for i in idx) for row in child.rows)
    return Relation(tuple(plan.attrs), rows)


def _eval_join(plan: Join, db) -> Relation:
    left = evaluate(plan.left, db)
    right = evaluate(plan.right, db)
    l_idx = [left.schema.index(a) for a, _ in plan.on]
    r_idx = [right.schema.index(b) for _, b in plan.on]
    l_pad = (NULL,) * len(left.schema)
    r_pad = (NULL,) * len(right.schema)

    rows: list[Row] = []
    matched_right = [False] * len(right.rows)
    for lrow in left.rows:
        matched = False
        for j, rrow in enumerate(right.rows):
            if all(
                sql_eq(lrow[i], rrow[k]) is True
                for i, k in zip(l_idx, r_idx)
            ):
                matched = True
                matched_right[j] = True
                rows.append(lrow + rrow)
        if not matched and plan.how in (LEFT, FULL):
            rows.append(lrow + r_pad)
    if plan.how in (RIGHT, FULL):
        for j, rrow in enumerate(right.rows):
            if not matched_right[j]:
                rows.append(l_pad + rrow)
    return Relation(left.schema + right.schema, tuple(rows))


# ---------------------------------------------------------------------------
# Pretty printing
# ---------------------------------------------------------------------------

def fmt_value(v) -> str:
    return "NULL" if v is NULL else repr(v)


def fmt_row(row: Row) -> str:
    return "(" + ", ".join(fmt_value(v) for v in row) + ")"


def fmt_relation(rel: Relation) -> str:
    head = "(" + ", ".join(rel.schema) + ")"
    if not rel.rows:
        return f"{head}: <empty>"
    lines = [f"{head}:"]
    for row, count in sorted(rel.multiset().items(), key=lambda kv: repr(kv[0])):
        suffix = f"  x{count}" if count > 1 else ""
        lines.append(f"  {fmt_row(row)}{suffix}")
    return "\n".join(lines)
