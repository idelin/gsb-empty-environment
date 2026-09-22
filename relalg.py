"""Relational tables and operators over local synthetic data.

No SQL engine is used: tables are plain Python objects and operators are
plain generators.

Semantics
---------
* NULL is represented by ``None`` and follows SQL three-valued logic:
  any comparison involving NULL evaluates to UNKNOWN (``None`` here).
* Relations are multisets (bags): duplicate rows are significant.  They
  are preserved by scan/filter/project and multiplied by joins.
* A filter keeps a row only when the predicate is TRUE; rows whose
  predicate is FALSE or UNKNOWN are dropped.
* Join keys that are NULL never match (``NULL = NULL`` is UNKNOWN).
"""
from __future__ import annotations

from dataclasses import dataclass

NULL = None


# --- SQL three-valued logic -------------------------------------------------
def _cmp(a, b, op):
    if a is None or b is None:
        return None  # UNKNOWN
    return op(a, b)


def eq(a, b):
    return _cmp(a, b, lambda x, y: x == y)


def ne(a, b):
    return _cmp(a, b, lambda x, y: x != y)


def lt(a, b):
    return _cmp(a, b, lambda x, y: x < y)


def le(a, b):
    return _cmp(a, b, lambda x, y: x <= y)


def gt(a, b):
    return _cmp(a, b, lambda x, y: x > y)


def ge(a, b):
    return _cmp(a, b, lambda x, y: x >= y)


def and_(x, y):
    if x is False or y is False:
        return False
    if x is None or y is None:
        return None
    return True


def or_(x, y):
    if x is True or y is True:
        return True
    if x is None or y is None:
        return None
    return False


def not_(x):
    return None if x is None else not x


# --- tables -----------------------------------------------------------------
@dataclass
class Table:
    """A local synthetic relation.  ``rows`` is a bag: duplicates matter."""

    name: str
    schema: tuple
    rows: list

    def __post_init__(self):
        self.schema = tuple(self.schema)
        self.rows = [tuple(r) for r in self.rows]
        for row in self.rows:
            if len(row) != len(self.schema):
                raise ValueError(
                    f"{self.name}: row {row!r} does not match schema {self.schema!r}"
                )

    def scan(self):
        return iter(self.rows)


# --- operators (all multiset-preserving) ------------------------------------
def filter_rows(rows, pred):
    """Keep only rows for which pred(row) is TRUE (not FALSE/UNKNOWN)."""
    for row in rows:
        if pred(row) is True:
            yield row


def project_rows(rows, indexes):
    for row in rows:
        yield tuple(row[i] for i in indexes)


def hash_join_rows(left_rows, right_rows, left_idx, right_idx):
    """Equi-join with bag semantics; NULL keys never match."""
    buckets = {}
    for r in right_rows:
        key = tuple(r[i] for i in right_idx)
        if any(v is None for v in key):
            continue
        buckets.setdefault(key, []).append(r)
    for l in left_rows:
        key = tuple(l[i] for i in left_idx)
        if any(v is None for v in key):
            continue
        for r in buckets.get(key, ()):
            yield l + r
