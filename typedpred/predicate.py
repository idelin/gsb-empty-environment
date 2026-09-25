"""Typed predicate tree with row-level and block-level evaluation.

Row evaluation uses three-valued logic (TVL): any comparison involving null
is UNKNOWN; NaN follows IEEE-754 (all comparisons False except !=).

Block evaluation maps a predicate plus column statistics to a TVL verdict:
  * TRUE    -> every row in the block is TRUE
  * FALSE   -> no row in the block can be TRUE  (only then may we skip)
  * UNKNOWN -> anything else (must scan)

The block rules are deliberately conservative: UNKNOWN always means "scan".
"""

from __future__ import annotations

import dataclasses
import operator
from typing import Any, Mapping

from .batch import ColumnStats
from .tvl import TVL
from .types import Schema, Type, TypeMismatchError, comparable, type_of_literal

_OPS = {
    ">": operator.gt,
    ">=": operator.ge,
    "<": operator.lt,
    "<=": operator.le,
    "=": operator.eq,
    "!=": operator.ne,
}


@dataclasses.dataclass(frozen=True)
class Verdict:
    value: TVL
    why: str


class Predicate:
    """Base class. Subclasses implement bind/eval_row/eval_block."""

    def bind(self, schema: Schema) -> None:
        raise NotImplementedError

    def eval_row(self, row: Mapping[str, Any]) -> TVL:
        raise NotImplementedError

    def eval_block(self, stats: Mapping[str, ColumnStats]) -> Verdict:
        raise NotImplementedError

    def __and__(self, other: "Predicate") -> "And":
        return And(self, other)

    def __or__(self, other: "Predicate") -> "Or":
        return Or(self, other)

    def __invert__(self) -> "Not":
        return Not(self)


class Col(Predicate):
    """Column reference; supports Col('x') > 2 style construction."""

    def __init__(self, name: str) -> None:
        self.name = name
        self.type: Type | None = None

    def bind(self, schema: Schema) -> None:
        self.type = schema.type_of(self.name)

    def _cmp(self, op: str, other: Any) -> "Compare":
        return Compare(self, op, Lit(other))

    def __gt__(self, other: Any) -> "Compare":
        return self._cmp(">", other)

    def __ge__(self, other: Any) -> "Compare":
        return self._cmp(">=", other)

    def __lt__(self, other: Any) -> "Compare":
        return self._cmp("<", other)

    def __le__(self, other: Any) -> "Compare":
        return self._cmp("<=", other)

    def eq(self, other: Any) -> "Compare":
        return self._cmp("=", other)

    def ne(self, other: Any) -> "Compare":
        return self._cmp("!=", other)

    def is_null(self) -> "IsNull":
        return IsNull(self)

    def is_not_null(self) -> "IsNotNull":
        return IsNotNull(self)

    def eval_row(self, row: Mapping[str, Any]) -> TVL:  # pragma: no cover
        raise TypeError("a bare column is not a boolean predicate")

    def eval_block(self, stats: Mapping[str, ColumnStats]) -> Verdict:  # pragma: no cover
        raise TypeError("a bare column is not a boolean predicate")

    def __repr__(self) -> str:
        return f"Col({self.name!r})"


class Lit(Predicate):
    def __init__(self, value: Any) -> None:
        if value is None:
            raise TypeMismatchError(
                "null literal in a comparison is not allowed; use IS NULL"
            )
        self.value = value
        self.type = type_of_literal(value)

    def __repr__(self) -> str:
        return f"Lit({self.value!r})"


class Compare(Predicate):
    def __init__(self, left: Col, op: str, right: Lit) -> None:
        if op not in _OPS:
            raise ValueError(f"unsupported operator {op!r}")
        self.left = left
        self.op = op
        self.right = right

    def bind(self, schema: Schema) -> None:
        self.left.bind(schema)
        if not comparable(self.left.type, self.right.type):
            raise TypeMismatchError(
                f"cannot compare {self.left.name}: {self.left.type.value} "
                f"with literal {self.right.value!r}: {self.right.type.value}"
            )

    def eval_row(self, row: Mapping[str, Any]) -> TVL:
        value = row[self.left.name]
        if value is None:
            return TVL.UNKNOWN
        # IEEE-754: NaN comparisons fall out of the Python operators
        # (NaN != c is True, everything else is False).
        return TVL.of(_OPS[self.op](value, self.right.value))

    def eval_block(self, stats: Mapping[str, ColumnStats]) -> Verdict:
        st = stats[self.left.name]
        c = self.right.value
        label = f"{self.left.name} {self.op} {c!r}"

        if st.valid_count == 0:
            # Only nulls (UNKNOWN) and/or NaNs in this block.
            if self.op == "!=" and st.nan_count > 0:
                if st.null_count == 0:
                    return Verdict(TVL.TRUE, f"{label}: all rows are NaN, NaN != {c!r}")
                return Verdict(
                    TVL.UNKNOWN,
                    f"{label}: NaN rows satisfy !=, null rows are UNKNOWN",
                )
            return Verdict(
                TVL.FALSE,
                f"{label}: block has no valid values "
                f"({st.null_count} null, {st.nan_count} NaN); "
                "null -> UNKNOWN, NaN comparison -> FALSE",
            )

        mn, mx = st.min, st.max
        if self.op == ">":
            may, must_valid = mx > c, mn > c
        elif self.op == ">=":
            may, must_valid = mx >= c, mn >= c
        elif self.op == "<":
            may, must_valid = mn < c, mx < c
        elif self.op == "<=":
            may, must_valid = mn <= c, mx <= c
        elif self.op == "=":
            may, must_valid = mn <= c <= mx, (mn == mx == c)
        else:  # "!="
            may = (not (mn == mx == c)) or st.nan_count > 0
            must_valid = mx < c or mn > c

        if not may:
            return Verdict(
                TVL.FALSE,
                f"{label}: valid range [{mn!r}, {mx!r}] (NaN excluded) "
                f"cannot satisfy the predicate",
            )
        nan_ok = self.op == "!="  # NaN != c is TRUE; other ops: FALSE
        all_rows = st.null_count == 0 and (st.nan_count == 0 or nan_ok)
        if must_valid and all_rows:
            return Verdict(
                TVL.TRUE,
                f"{label}: every row satisfies it "
                f"(range [{mn!r}, {mx!r}], no nulls"
                f"{', NaN rows match !=' if st.nan_count else ''})",
            )
        return Verdict(
            TVL.UNKNOWN,
            f"{label}: range [{mn!r}, {mx!r}] may or may not match; must scan",
        )

    def __repr__(self) -> str:
        return f"({self.left.name} {self.op} {self.right.value!r})"


class IsNull(Predicate):
    def __init__(self, col: Col) -> None:
        self.col = col

    def bind(self, schema: Schema) -> None:
        self.col.bind(schema)

    def eval_row(self, row: Mapping[str, Any]) -> TVL:
        # NaN is a value, not null: IS NULL is FALSE for NaN.
        return TVL.of(row[self.col.name] is None)

    def eval_block(self, stats: Mapping[str, ColumnStats]) -> Verdict:
        st = stats[self.col.name]
        label = f"{self.col.name} IS NULL"
        if st.null_count == 0:
            return Verdict(TVL.FALSE, f"{label}: block has no nulls")
        if st.null_count == st.count:
            return Verdict(TVL.TRUE, f"{label}: all {st.count} rows are null")
        return Verdict(
            TVL.UNKNOWN, f"{label}: {st.null_count}/{st.count} rows are null"
        )

    def __repr__(self) -> str:
        return f"({self.col.name} IS NULL)"


class IsNotNull(Predicate):
    def __init__(self, col: Col) -> None:
        self.col = col

    def bind(self, schema: Schema) -> None:
        self.col.bind(schema)

    def eval_row(self, row: Mapping[str, Any]) -> TVL:
        return TVL.of(row[self.col.name] is not None)

    def eval_block(self, stats: Mapping[str, ColumnStats]) -> Verdict:
        st = stats[self.col.name]
        label = f"{self.col.name} IS NOT NULL"
        non_null = st.count - st.null_count
        if non_null == 0:
            return Verdict(TVL.FALSE, f"{label}: all rows are null")
        if st.null_count == 0:
            return Verdict(TVL.TRUE, f"{label}: block has no nulls")
        return Verdict(
            TVL.UNKNOWN, f"{label}: {non_null}/{st.count} rows are non-null"
        )

    def __repr__(self) -> str:
        return f"({self.col.name} IS NOT NULL)"


class And(Predicate):
    def __init__(self, *parts: Predicate) -> None:
        self.parts = parts

    def bind(self, schema: Schema) -> None:
        for p in self.parts:
            p.bind(schema)

    def eval_row(self, row: Mapping[str, Any]) -> TVL:
        result = TVL.TRUE
        for p in self.parts:
            result = result & p.eval_row(row)
        return result

    def eval_block(self, stats: Mapping[str, ColumnStats]) -> Verdict:
        verdicts = [p.eval_block(stats) for p in self.parts]
        value = TVL.TRUE
        for v in verdicts:
            value = value & v.value
        why = "; ".join(v.why for v in verdicts)
        return Verdict(value, f"AND[{why}]")

    def __repr__(self) -> str:
        return "(" + " AND ".join(repr(p) for p in self.parts) + ")"


class Or(Predicate):
    def __init__(self, *parts: Predicate) -> None:
        self.parts = parts

    def bind(self, schema: Schema) -> None:
        for p in self.parts:
            p.bind(schema)

    def eval_row(self, row: Mapping[str, Any]) -> TVL:
        result = TVL.FALSE
        for p in self.parts:
            result = result | p.eval_row(row)
        return result

    def eval_block(self, stats: Mapping[str, ColumnStats]) -> Verdict:
        verdicts = [p.eval_block(stats) for p in self.parts]
        value = TVL.FALSE
        for v in verdicts:
            value = value | v.value
        why = "; ".join(v.why for v in verdicts)
        return Verdict(value, f"OR[{why}]")

    def __repr__(self) -> str:
        return "(" + " OR ".join(repr(p) for p in self.parts) + ")"


class Not(Predicate):
    def __init__(self, inner: Predicate) -> None:
        self.inner = inner

    def bind(self, schema: Schema) -> None:
        self.inner.bind(schema)

    def eval_row(self, row: Mapping[str, Any]) -> TVL:
        # NOT UNKNOWN is UNKNOWN.
        return ~self.inner.eval_row(row)

    def eval_block(self, stats: Mapping[str, ColumnStats]) -> Verdict:
        v = self.inner.eval_block(stats)
        return Verdict(~v.value, f"NOT({v.why})")

    def __repr__(self) -> str:
        return f"(NOT {self.inner!r})"
