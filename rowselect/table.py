"""Typed batch tables and bitmap-producing predicates.

Tables are ingested as batches of rows against a fixed schema with column
types (``int``, ``bool``, ``float``, ``str``). ``None`` is SQL-style null
and is distinct from ``float("nan")``: null propagates through comparisons
(three-valued logic, so the row is not selected), while NaN is an ordinary
float value that simply compares unequal to everything, including itself.
Integers are kept as Python ints, so arithmetic and comparisons are exact
at any magnitude (no float rounding). No external query engine is used.
"""

from __future__ import annotations

import math
import operator

from .bitmap import Bitmap

COLUMN_TYPES = ("int", "bool", "float", "str")


def _check_type(value, col_type: str, name: str) -> None:
    ok = {
        "int": lambda v: type(v) is int,
        "bool": lambda v: type(v) is bool,
        "float": lambda v: type(v) in (int, float) and type(v) is not bool,
        "str": lambda v: type(v) is str,
    }[col_type](value)
    if not ok:
        raise TypeError(
            f"column {name!r} expects {col_type}, got {type(value).__name__}"
        )


class Schema:
    """Ordered mapping of column name -> column type."""

    def __init__(self, columns):
        self.columns = list(columns)
        names = [n for n, _ in self.columns]
        if len(set(names)) != len(names):
            raise ValueError("duplicate column names")
        for _, t in self.columns:
            if t not in COLUMN_TYPES:
                raise ValueError(f"unknown column type {t!r}")
        self._types = dict(self.columns)

    def type_of(self, name: str) -> str:
        try:
            return self._types[name]
        except KeyError:
            raise KeyError(f"unknown column {name!r}") from None

    def validate_row(self, row) -> tuple:
        if isinstance(row, dict):
            row = tuple(row.get(n) for n, _ in self.columns)
        row = tuple(row)
        if len(row) != len(self.columns):
            raise ValueError(
                f"row has {len(row)} values, schema has {len(self.columns)} columns"
            )
        for value, (name, col_type) in zip(row, self.columns):
            if value is not None:  # None is null: allowed for any column
                _check_type(value, col_type, name)
        return row

    def __eq__(self, other):
        return isinstance(other, Schema) and self.columns == other.columns

    def __repr__(self):
        cols = ", ".join(f"{n}: {t}" for n, t in self.columns)
        return f"Schema({cols})"


class Batch:
    """A typed batch of rows conforming to a schema."""

    def __init__(self, schema: Schema, rows):
        self.schema = schema
        self.rows = [schema.validate_row(r) for r in rows]

    def __len__(self):
        return len(self.rows)


class Table:
    """A table ingested as a sequence of typed batches."""

    def __init__(self, schema: Schema, batches=()):
        self.schema = schema
        self.batches: list[Batch] = []
        self.num_rows = 0
        for batch in batches:
            self.add_batch(batch)

    def add_batch(self, batch: Batch) -> None:
        if batch.schema != self.schema:
            raise ValueError("batch schema does not match table schema")
        self.batches.append(batch)
        self.num_rows += len(batch)

    def column(self, name: str) -> list:
        idx = [n for n, _ in self.schema.columns].index(name)
        return [row[idx] for b in self.batches for row in b.rows]

    def where(self, expr: "Expr") -> Bitmap:
        """Evaluate a predicate expression into a selection bitmap."""
        return expr.evaluate(self)

    def fetch(self, bitmap: Bitmap) -> list[tuple]:
        """Materialize the rows selected by a bitmap."""
        if bitmap.length != self.num_rows:
            raise ValueError(
                f"bitmap length {bitmap.length} does not match "
                f"table row count {self.num_rows}"
            )
        rows = [row for b in self.batches for row in b.rows]
        return [rows[i] for i in bitmap]


# ----------------------------------------------------------------------
# predicate expressions
# ----------------------------------------------------------------------
_CMP_OPS = {
    "==": operator.eq,
    "!=": operator.ne,
    "<": operator.lt,
    "<=": operator.le,
    ">": operator.gt,
    ">=": operator.ge,
}


class Expr:
    """A predicate that evaluates to a selection bitmap over a table."""

    def evaluate(self, table: Table) -> Bitmap:
        raise NotImplementedError

    def __and__(self, other: "Expr") -> "Expr":
        return _Logic("and", self, other)

    def __or__(self, other: "Expr") -> "Expr":
        return _Logic("or", self, other)

    def __invert__(self) -> "Expr":
        return _Not(self)


def _values_of(term, table: Table) -> list:
    if isinstance(term, Col):
        return table.column(term.name)
    return [term] * table.num_rows


class Col(Expr):
    """A column reference; comparisons build predicate expressions."""

    def __init__(self, name: str):
        self.name = name

    def evaluate(self, table: Table) -> Bitmap:
        # A bare column is truthy-filtered (null/0/""/False are not selected).
        return Bitmap.from_indices(
            table.num_rows,
            (i for i, v in enumerate(table.column(self.name)) if v),
        )

    def _cmp(self, op: str, other) -> "Expr":
        return _Compare(op, self, other)

    def __eq__(self, other):  # type: ignore[override]
        return self._cmp("==", other)

    def __ne__(self, other):  # type: ignore[override]
        return self._cmp("!=", other)

    def __lt__(self, other):
        return self._cmp("<", other)

    def __le__(self, other):
        return self._cmp("<=", other)

    def __gt__(self, other):
        return self._cmp(">", other)

    def __ge__(self, other):
        return self._cmp(">=", other)

    def __hash__(self):
        return hash(("Col", self.name))

    def is_null(self) -> "Expr":
        return _NullTest(self.name, negate=False)

    def is_not_null(self) -> "Expr":
        return _NullTest(self.name, negate=True)

    def is_nan(self) -> "Expr":
        return _NanTest(self.name)


class _Compare(Expr):
    def __init__(self, op: str, left, right):
        self.op, self.left, self.right = op, left, right

    def evaluate(self, table: Table) -> Bitmap:
        fn = _CMP_OPS[self.op]
        left = _values_of(self.left, table)
        right = _values_of(self.right, table)
        selected = []
        for i, (a, b) in enumerate(zip(left, right)):
            if a is None or b is None:
                continue  # null propagates: three-valued logic
            if fn(a, b):
                selected.append(i)
        return Bitmap.from_indices(table.num_rows, selected)


class _NullTest(Expr):
    def __init__(self, name: str, negate: bool):
        self.name, self.negate = name, negate

    def evaluate(self, table: Table) -> Bitmap:
        values = table.column(self.name)
        return Bitmap.from_indices(
            table.num_rows,
            (i for i, v in enumerate(values) if (v is not None) == self.negate),
        )


class _NanTest(Expr):
    def __init__(self, name: str):
        self.name = name

    def evaluate(self, table: Table) -> Bitmap:
        values = table.column(self.name)
        return Bitmap.from_indices(
            table.num_rows,
            (
                i
                for i, v in enumerate(values)
                if isinstance(v, float) and math.isnan(v)
            ),
        )


class _Logic(Expr):
    def __init__(self, op: str, left: Expr, right: Expr):
        self.op, self.left, self.right = op, left, right

    def evaluate(self, table: Table) -> Bitmap:
        a = self.left.evaluate(table)
        b = self.right.evaluate(table)
        return a & b if self.op == "and" else a | b


class _Not(Expr):
    def __init__(self, inner: Expr):
        self.inner = inner

    def evaluate(self, table: Table) -> Bitmap:
        return ~self.inner.evaluate(table)


def col(name: str) -> Col:
    return Col(name)
