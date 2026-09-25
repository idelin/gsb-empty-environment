"""Typed record batches with composable bitmap selection.

Tables are ingested as batches of typed columns.  Column expressions build
predicates that evaluate to compressed :class:`Bitmap` selections; both
predicates and bitmaps compose with ``&``, ``|`` and ``~``.

Semantics:

* null (``None``) and NaN are distinct: null means "missing", NaN is a
  present float value.  Any comparison involving a null is false; NaN
  follows IEEE rules (``nan == nan`` is false, ``nan != nan`` is true).
* Integer columns use Python arbitrary-precision ints, so arithmetic and
  comparisons are exact at any magnitude.
* No external query engine is used -- predicates compile down to plain
  Python loops that emit compressed bitmaps.
"""

from __future__ import annotations

import math
import operator
from enum import Enum

from bitmap import Bitmap, BitmapLengthError


class ColType(Enum):
    INT = "int"
    FLOAT = "float"
    STRING = "string"
    BOOL = "bool"


class SchemaError(TypeError):
    """Raised when a batch or expression does not match the schema."""


def _is_int(v):
    return isinstance(v, int) and not isinstance(v, bool)


def _is_number(v):
    return isinstance(v, (int, float)) and not isinstance(v, bool)


_VALIDATORS = {
    ColType.INT: _is_int,
    ColType.FLOAT: _is_number,
    ColType.STRING: lambda v: isinstance(v, str),
    ColType.BOOL: lambda v: isinstance(v, bool),
}


class Table:
    """A typed columnar table ingested batch by batch."""

    def __init__(self, schema):
        self._schema = {name: ColType(t) for name, t in schema.items()}
        if not self._schema:
            raise SchemaError("schema must define at least one column")
        self._columns = {name: [] for name in self._schema}
        self._nrows = 0
        self._nbatches = 0

    @property
    def num_rows(self):
        return self._nrows

    @property
    def num_batches(self):
        return self._nbatches

    @property
    def schema(self):
        return dict(self._schema)

    def add_batch(self, batch):
        """Append one batch: a dict mapping column name to value list."""
        unknown = set(batch) - set(self._schema)
        missing = set(self._schema) - set(batch)
        if unknown or missing:
            raise SchemaError(
                f"batch does not match schema: unknown={sorted(unknown)} "
                f"missing={sorted(missing)}")
        typed = {}
        nrows = None
        for name, values in batch.items():
            values = list(values)
            if nrows is None:
                nrows = len(values)
            elif len(values) != nrows:
                raise SchemaError("ragged batch: columns differ in length")
            coltype = self._schema[name]
            check = _VALIDATORS[coltype]
            out = []
            for v in values:
                if v is None:
                    out.append(None)
                elif check(v):
                    out.append(float(v) if coltype is ColType.FLOAT else v)
                else:
                    raise SchemaError(
                        f"value {v!r} does not match column {name!r} "
                        f"of type {coltype.value}")
            typed[name] = out
        for name, out in typed.items():
            self._columns[name].extend(out)
        self._nrows += nrows or 0
        self._nbatches += 1
        return self

    def col(self, name):
        """Column expression builder for ``name``."""
        if name not in self._schema:
            raise SchemaError(f"unknown column {name!r}")
        return Col(name)

    def where(self, predicate):
        """Evaluate a predicate to a selection :class:`Bitmap`."""
        if not isinstance(predicate, Predicate):
            raise TypeError("where() expects a Predicate")
        return predicate.eval(self)

    def filter(self, selection):
        """Return the selected rows as a list of dicts.

        ``selection`` may be a Predicate or a Bitmap whose length must
        equal the table's row count.
        """
        bm = self.where(selection) if isinstance(selection, Predicate) \
            else selection
        if not isinstance(bm, Bitmap):
            raise TypeError("filter() expects a Predicate or Bitmap")
        if len(bm) != self._nrows:
            raise BitmapLengthError(
                f"selection bitmap has length {len(bm)} but table has "
                f"{self._nrows} rows")
        names = list(self._schema)
        cols = [self._columns[n] for n in names]
        return [dict(zip(names, (c[i] for c in cols))) for i in bm]


# ---------------------------------------------------------------------------
# Expressions and predicates
# ---------------------------------------------------------------------------

def _ensure_expr(value):
    return value if isinstance(value, Expr) else _Const(value)


class Expr:
    """Row-wise expression; evaluates against a Table to a value list."""

    def eval(self, table):
        raise NotImplementedError

    # arithmetic (exact for integers, IEEE for floats; null propagates)
    def _arith(self, other, op, symbol):
        return _Arith(self, _ensure_expr(other), op, symbol)

    def __add__(self, other):
        return self._arith(other, operator.add, "+")

    def __radd__(self, other):
        return self._arith(other, lambda a, b: b + a, "+")

    def __sub__(self, other):
        return self._arith(other, operator.sub, "-")

    def __rsub__(self, other):
        return self._arith(other, lambda a, b: b - a, "-")

    def __mul__(self, other):
        return self._arith(other, operator.mul, "*")

    def __rmul__(self, other):
        return self._arith(other, lambda a, b: b * a, "*")

    # comparisons -> Predicate (null makes them false; NaN follows IEEE)
    def _cmp(self, other, op, symbol):
        return _Cmp(self, _ensure_expr(other), op, symbol)

    def __eq__(self, other):
        return self._cmp(other, operator.eq, "==")

    def __ne__(self, other):
        return self._cmp(other, operator.ne, "!=")

    def __lt__(self, other):
        return self._cmp(other, operator.lt, "<")

    def __le__(self, other):
        return self._cmp(other, operator.le, "<=")

    def __gt__(self, other):
        return self._cmp(other, operator.gt, ">")

    def __ge__(self, other):
        return self._cmp(other, operator.ge, ">=")

    # null / NaN tests
    def is_null(self):
        return _NullTest(self, negate=False)

    def is_valid(self):
        return _NullTest(self, negate=True)

    def is_nan(self):
        return _NanTest(self)


class Col(Expr):
    def __init__(self, name):
        self.name = name

    def eval(self, table):
        try:
            return table._columns[self.name]
        except KeyError:
            raise SchemaError(f"unknown column {self.name!r}") from None

    def __repr__(self):
        return f"col({self.name})"


class _Const(Expr):
    def __init__(self, value):
        self.value = value

    def eval(self, table):
        return [self.value] * table.num_rows


class _Arith(Expr):
    def __init__(self, left, right, op, symbol):
        self.left = left
        self.right = right
        self.op = op
        self.symbol = symbol

    def eval(self, table):
        lhs = self.left.eval(table)
        rhs = self.right.eval(table)
        out = []
        for x, y in zip(lhs, rhs):
            if x is None or y is None:
                out.append(None)
            elif _is_number(x) and _is_number(y):
                out.append(self.op(x, y))
            else:
                raise SchemaError(
                    f"arithmetic '{self.symbol}' requires numeric operands, "
                    f"got {x!r} and {y!r}")
        return out


class Predicate:
    """Boolean expression evaluating to a selection :class:`Bitmap`."""

    def eval(self, table):
        raise NotImplementedError

    def __and__(self, other):
        return _BoolOp("and", self, other)

    def __or__(self, other):
        return _BoolOp("or", self, other)

    def __invert__(self):
        return _NotOp(self)


class _Cmp(Predicate):
    def __init__(self, left, right, op, symbol):
        self.left = left
        self.right = right
        self.op = op
        self.symbol = symbol

    def eval(self, table):
        lhs = self.left.eval(table)
        rhs = self.right.eval(table)
        flags = []
        for x, y in zip(lhs, rhs):
            if x is None or y is None:
                flags.append(False)  # null never matches a comparison
            else:
                flags.append(bool(self.op(x, y)))  # NaN: IEEE semantics
        return Bitmap.from_bools(flags)


class _NullTest(Predicate):
    def __init__(self, expr, negate):
        self.expr = expr
        self.negate = negate

    def eval(self, table):
        flags = [v is None for v in self.expr.eval(table)]
        if self.negate:
            flags = [not f for f in flags]
        return Bitmap.from_bools(flags)


class _NanTest(Predicate):
    def __init__(self, expr):
        self.expr = expr

    def eval(self, table):
        return Bitmap.from_bools(
            [isinstance(v, float) and math.isnan(v)
             for v in self.expr.eval(table)])


class _BoolOp(Predicate):
    def __init__(self, kind, left, right):
        self.kind = kind
        self.left = left
        self.right = right

    def eval(self, table):
        a = self.left.eval(table)
        b = self.right.eval(table)
        return a & b if self.kind == "and" else a | b


class _NotOp(Predicate):
    def __init__(self, operand):
        self.operand = operand

    def eval(self, table):
        # Bitmap.__invert__ flips only the valid row range; no tail leak.
        return ~self.operand.eval(table)
