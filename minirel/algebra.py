"""Core relational values: base tables as bags of tuples, explicit NULL,
and a small expression language with SQL three-valued logic (3VL).

Semantics are explicit and engine-free:
- NULL is ``None``. Any comparison or arithmetic involving NULL yields
  UNKNOWN (also represented as ``None``).
- Filters keep a row only when the predicate is TRUE (UNKNOWN is rejected).
- Equi-join keys that are NULL never match (not even NULL = NULL).
- Relations are multisets: duplicate rows are preserved everywhere.
"""
from __future__ import annotations

from dataclasses import dataclass

NULL = None  # explicit SQL NULL marker


@dataclass(frozen=True)
class ColRef:
    """A column qualified by its source alias, so same-named columns from
    different tables (e.g. e.name vs m.name) stay distinct."""
    source: str
    name: str

    def __str__(self) -> str:
        return f"{self.source}.{self.name}"


class Table:
    """A base relation with bag (multiset) semantics: duplicate rows kept."""

    def __init__(self, name: str, columns: list[str], rows: list[tuple]):
        self.name = name
        self.columns = list(columns)
        self.rows: list[tuple] = []
        for row in rows:
            row = tuple(row)
            if len(row) != len(self.columns):
                raise ValueError(
                    f"{name}: row {row!r} has {len(row)} values, "
                    f"expected {len(self.columns)}"
                )
            self.rows.append(row)


# ---------------------------------------------------------------------------
# Expressions
# ---------------------------------------------------------------------------

class Expr:
    def columns(self) -> set[ColRef]:
        raise NotImplementedError

    def eval(self, ctx: dict[ColRef, object]):
        raise NotImplementedError


@dataclass(frozen=True)
class Col(Expr):
    ref: ColRef

    def columns(self) -> set[ColRef]:
        return {self.ref}

    def eval(self, ctx):
        return ctx[self.ref]

    def __str__(self) -> str:
        return str(self.ref)


@dataclass(frozen=True)
class Lit(Expr):
    value: object

    def columns(self) -> set[ColRef]:
        return set()

    def eval(self, ctx):
        return self.value


_COMPARE = {
    "=": lambda a, b: a == b,
    "!=": lambda a, b: a != b,
    "<": lambda a, b: a < b,
    "<=": lambda a, b: a <= b,
    ">": lambda a, b: a > b,
    ">=": lambda a, b: a >= b,
}

_ARITH = {
    "+": lambda a, b: a + b,
    "-": lambda a, b: a - b,
    "*": lambda a, b: a * b,
}


@dataclass(frozen=True)
class Cmp(Expr):
    """Comparison with NULL propagation: any NULL operand -> UNKNOWN."""
    op: str
    left: Expr
    right: Expr

    def __post_init__(self):
        if self.op not in _COMPARE:
            raise ValueError(f"unknown comparison op {self.op!r}")

    def columns(self) -> set[ColRef]:
        return self.left.columns() | self.right.columns()

    def eval(self, ctx):
        left = self.left.eval(ctx)
        right = self.right.eval(ctx)
        if left is None or right is None:
            return None  # UNKNOWN
        return _COMPARE[self.op](left, right)


@dataclass(frozen=True)
class Arith(Expr):
    """Arithmetic with NULL propagation (implicit column dependencies)."""
    op: str
    left: Expr
    right: Expr

    def __post_init__(self):
        if self.op not in _ARITH:
            raise ValueError(f"unknown arithmetic op {self.op!r}")

    def columns(self) -> set[ColRef]:
        return self.left.columns() | self.right.columns()

    def eval(self, ctx):
        left = self.left.eval(ctx)
        right = self.right.eval(ctx)
        if left is None or right is None:
            return None
        return _ARITH[self.op](left, right)


@dataclass(frozen=True)
class And(Expr):
    left: Expr
    right: Expr

    def columns(self) -> set[ColRef]:
        return self.left.columns() | self.right.columns()

    def eval(self, ctx):
        left = self.left.eval(ctx)
        right = self.right.eval(ctx)
        if left is False or right is False:
            return False
        if left is None or right is None:
            return None
        return True


@dataclass(frozen=True)
class Or(Expr):
    left: Expr
    right: Expr

    def columns(self) -> set[ColRef]:
        return self.left.columns() | self.right.columns()

    def eval(self, ctx):
        left = self.left.eval(ctx)
        right = self.right.eval(ctx)
        if left is True or right is True:
            return True
        if left is None or right is None:
            return None
        return False


@dataclass(frozen=True)
class Not(Expr):
    expr: Expr

    def columns(self) -> set[ColRef]:
        return self.expr.columns()

    def eval(self, ctx):
        value = self.expr.eval(ctx)
        if value is None:
            return None
        return not value


@dataclass(frozen=True)
class IsNull(Expr):
    expr: Expr
    negated: bool = False

    def columns(self) -> set[ColRef]:
        return self.expr.columns()

    def eval(self, ctx):
        result = self.expr.eval(ctx) is None
        return (not result) if self.negated else result
