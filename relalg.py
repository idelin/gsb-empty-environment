"""Relational algebra core with explicit NULL, three-valued logic (3VL)
and bag (multiset) semantics. Pure standard library, no SQL engine.

Conventions
-----------
- NULL is represented by Python ``None``.
- Predicates evaluate to a three-valued truth value: TV.TRUE, TV.FALSE
  or TV.UNKNOWN. A SELECT (WHERE) keeps a row only when the predicate
  evaluates to TV.TRUE (UNKNOWN and FALSE are both rejected).
- Relations are bags: ``rows`` is a list and duplicates are meaningful.
  Join multiplicity is the product of the multiplicities of matching
  rows; filters never merge or deduplicate rows.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum

NULL = None


class TV(Enum):
    """Three-valued logic truth values."""

    TRUE = 1
    FALSE = 2
    UNKNOWN = 3

    def __and__(self, other: "TV") -> "TV":
        if self is TV.FALSE or other is TV.FALSE:
            return TV.FALSE
        if self is TV.TRUE and other is TV.TRUE:
            return TV.TRUE
        return TV.UNKNOWN

    def __or__(self, other: "TV") -> "TV":
        if self is TV.TRUE or other is TV.TRUE:
            return TV.TRUE
        if self is TV.FALSE and other is TV.FALSE:
            return TV.FALSE
        return TV.UNKNOWN

    def __invert__(self) -> "TV":
        if self is TV.TRUE:
            return TV.FALSE
        if self is TV.FALSE:
            return TV.TRUE
        return TV.UNKNOWN


# ---------------------------------------------------------------------------
# Predicate expression AST
# ---------------------------------------------------------------------------

class Expr:
    """Base class of predicate/value expressions."""

    def eval(self, row: dict):
        raise NotImplementedError

    def columns(self) -> set:
        """Column names referenced by this expression."""
        raise NotImplementedError


@dataclass(frozen=True)
class Lit(Expr):
    value: object

    def eval(self, row: dict):
        return self.value

    def columns(self) -> set:
        return set()


@dataclass(frozen=True)
class Col(Expr):
    name: str

    def eval(self, row: dict):
        return row[self.name]

    def columns(self) -> set:
        return {self.name}


@dataclass(frozen=True)
class Arith(Expr):
    """Arithmetic on values; NULL propagates (result is NULL)."""

    op: str  # '+', '-', '*'
    left: Expr
    right: Expr

    def eval(self, row: dict):
        a = self.left.eval(row)
        b = self.right.eval(row)
        if a is None or b is None:
            return None
        if self.op == "+":
            return a + b
        if self.op == "-":
            return a - b
        if self.op == "*":
            return a * b
        raise ValueError(self.op)

    def columns(self) -> set:
        return self.left.columns() | self.right.columns()


_CMP_OPS = {
    "=": lambda a, b: a == b,
    "!=": lambda a, b: a != b,
    "<": lambda a, b: a < b,
    "<=": lambda a, b: a <= b,
    ">": lambda a, b: a > b,
    ">=": lambda a, b: a >= b,
}


@dataclass(frozen=True)
class Cmp(Expr):
    """Comparison under 3VL: NULL on either side yields UNKNOWN."""

    op: str
    left: Expr
    right: Expr

    def eval(self, row: dict) -> TV:
        a = self.left.eval(row)
        b = self.right.eval(row)
        if a is None or b is None:
            return TV.UNKNOWN
        return TV.TRUE if _CMP_OPS[self.op](a, b) else TV.FALSE

    def columns(self) -> set:
        return self.left.columns() | self.right.columns()


@dataclass(frozen=True)
class IsNull(Expr):
    child: Expr

    def eval(self, row: dict) -> TV:
        return TV.TRUE if self.child.eval(row) is None else TV.FALSE

    def columns(self) -> set:
        return self.child.columns()


@dataclass(frozen=True)
class IsNotNull(Expr):
    child: Expr

    def eval(self, row: dict) -> TV:
        return TV.FALSE if self.child.eval(row) is None else TV.TRUE

    def columns(self) -> set:
        return self.child.columns()


@dataclass(frozen=True)
class And(Expr):
    left: Expr
    right: Expr

    def eval(self, row: dict) -> TV:
        return self.left.eval(row) & self.right.eval(row)

    def columns(self) -> set:
        return self.left.columns() | self.right.columns()


@dataclass(frozen=True)
class Or(Expr):
    left: Expr
    right: Expr

    def eval(self, row: dict) -> TV:
        return self.left.eval(row) | self.right.eval(row)

    def columns(self) -> set:
        return self.left.columns() | self.right.columns()


@dataclass(frozen=True)
class Not(Expr):
    child: Expr

    def eval(self, row: dict) -> TV:
        return ~self.child.eval(row)

    def columns(self) -> set:
        return self.child.columns()


# ---------------------------------------------------------------------------
# Relations (bags of rows) and operators
# ---------------------------------------------------------------------------

@dataclass
class Relation:
    """A bag relation: ``rows`` is a list, duplicates are meaningful."""

    name: str
    columns: tuple
    rows: list = field(default_factory=list)

    def __post_init__(self):
        for row in self.rows:
            if set(row) != set(self.columns):
                raise ValueError(
                    f"row {row} does not match schema {self.columns}"
                )


def select(rel: Relation, pred: Expr) -> Relation:
    """sigma_pred(rel): keep rows whose predicate is TRUE (3VL WHERE)."""
    return Relation(
        rel.name, rel.columns, [r for r in rel.rows if pred.eval(r) is TV.TRUE]
    )


def _merge(left_row: dict, right_row: dict) -> dict:
    merged = dict(left_row)
    merged.update(right_row)
    return merged


def inner_join(left: Relation, right: Relation, pred: Expr) -> Relation:
    """Theta inner join with bag semantics (multiplicity = product)."""
    rows = [
        _merge(lr, rr)
        for lr in left.rows
        for rr in right.rows
        if pred.eval(_merge(lr, rr)) is TV.TRUE
    ]
    return Relation(
        f"({left.name} JOIN {right.name})",
        left.columns + right.columns,
        rows,
    )


def left_join(left: Relation, right: Relation, pred: Expr) -> Relation:
    """Left outer join: unmatched left rows are padded with NULLs on the
    right (null-supplying) side. Each unmatched occurrence is preserved,
    so duplicate left rows each survive with multiplicity intact."""
    null_pad = {c: None for c in right.columns}
    rows = []
    for lr in left.rows:
        matched = False
        for rr in right.rows:
            merged = _merge(lr, rr)
            if pred.eval(merged) is TV.TRUE:
                rows.append(merged)
                matched = True
        if not matched:
            rows.append(_merge(lr, null_pad))
    return Relation(
        f"({left.name} LEFT JOIN {right.name})",
        left.columns + right.columns,
        rows,
    )


def _row_key(row: dict):
    # None (NULL) cannot be compared with values, so sort by a
    # NULL-aware, type-agnostic key.
    return tuple((k, v is None, repr(v)) for k, v in sorted(row.items()))


def bag_key(rel: Relation):
    """Canonical multiset key: sorted tuple of rows (order-insensitive,
    multiplicity-sensitive)."""
    return sorted(_row_key(r) for r in rel.rows)


def bag_equal(a: Relation, b: Relation) -> bool:
    return a.columns == b.columns and bag_key(a) == bag_key(b)


def format_table(rel: Relation) -> str:
    def fmt(v):
        return "NULL" if v is None else str(v)

    widths = [
        max(len(c), *(len(fmt(r[c])) for r in rel.rows)) if rel.rows else len(c)
        for c in rel.columns
    ]
    header = " | ".join(c.ljust(w) for c, w in zip(rel.columns, widths))
    sep = "-+-".join("-" * w for w in widths)
    body = [
        " | ".join(fmt(r[c]).ljust(w) for c, w in zip(rel.columns, widths))
        for r in rel.rows
    ]
    title = f"{rel.name}  ({len(rel.rows)} row(s), bag)"
    return "\n".join([title, header, sep, *body])
