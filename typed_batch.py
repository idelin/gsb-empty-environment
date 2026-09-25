"""Typed column batches: tables arrive as batches of typed columns.

Design rules:
- NULL is a dedicated sentinel, distinct from float NaN.
- INT columns use Python ints, so integer arithmetic is exact
  (no 64-bit overflow, no float rounding).
- No external query engine is used; aggregation is implemented here.
"""

from __future__ import annotations

import math
from enum import Enum


class ColType(Enum):
    INT = "int"        # exact integer, arbitrary precision
    FLOAT = "float"    # IEEE double; NaN is a value, not NULL
    BOOL = "bool"
    STRING = "string"


class _Null:
    """Singleton NULL sentinel. `NULL is NULL` and NULL != float('nan')."""

    _instance = None

    def __new__(cls) -> "_Null":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:  # pragma: no cover - cosmetic
        return "NULL"

    def __bool__(self) -> bool:
        raise TypeError("NULL has no truth value")


NULL = _Null()


def is_null(value) -> bool:
    return value is NULL


def is_nan(value) -> bool:
    return isinstance(value, float) and math.isnan(value)


def _validate_value(col_type: ColType, value):
    if value is NULL:
        return NULL
    if col_type is ColType.INT:
        if isinstance(value, bool) or not isinstance(value, int):
            raise TypeError(f"INT column rejects {value!r}")
        return value
    if col_type is ColType.FLOAT:
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise TypeError(f"FLOAT column rejects {value!r}")
        return float(value)
    if col_type is ColType.BOOL:
        if not isinstance(value, bool):
            raise TypeError(f"BOOL column rejects {value!r}")
        return value
    if col_type is ColType.STRING:
        if not isinstance(value, str):
            raise TypeError(f"STRING column rejects {value!r}")
        return value
    raise ValueError(f"unknown column type {col_type!r}")


class TypedBatch:
    """An immutable-schema batch of typed columns with equal row counts."""

    def __init__(self, schema: list[tuple[str, ColType]], columns: dict[str, list]):
        self.schema: tuple[tuple[str, ColType], ...] = tuple(schema)
        names = [name for name, _ in self.schema]
        if len(set(names)) != len(names):
            raise ValueError("duplicate column names in schema")
        if set(columns) != set(names):
            raise ValueError("columns do not match schema")
        self._types = dict(self.schema)
        self._columns: dict[str, tuple] = {}
        row_count = None
        for name, col_type in self.schema:
            values = tuple(_validate_value(col_type, v) for v in columns[name])
            if row_count is None:
                row_count = len(values)
            elif len(values) != row_count:
                raise ValueError("columns have different row counts")
            self._columns[name] = values
        self.nrows = row_count or 0

    @classmethod
    def from_rows(cls, schema: list[tuple[str, ColType]], rows: list[tuple]) -> "TypedBatch":
        names = [name for name, _ in schema]
        columns = {name: [] for name in names}
        for row in rows:
            if len(row) != len(names):
                raise ValueError(f"row {row!r} does not match schema width")
            for name, value in zip(names, row):
                columns[name].append(value)
        return cls(schema, columns)

    def column(self, name: str) -> tuple:
        return self._columns[name]

    def col_type(self, name: str) -> ColType:
        return self._types[name]

    def rows(self):
        names = [name for name, _ in self.schema]
        for i in range(self.nrows):
            yield tuple(self._columns[name][i] for name in names)

    def concat(self, other: "TypedBatch") -> "TypedBatch":
        """Append another batch; schemas must match exactly."""
        if self.schema != other.schema:
            raise ValueError(f"schema mismatch: {self.schema!r} vs {other.schema!r}")
        merged = {
            name: list(self._columns[name]) + list(other._columns[name])
            for name, _ in self.schema
        }
        return TypedBatch(list(self.schema), merged)


def group_sum_int(batch: TypedBatch, key_col: str, val_col: str) -> dict:
    """Exact integer group-by-sum.

    Rows with a NULL key or NULL value are skipped. Integer sums are
    exact (arbitrary precision). NaN cannot appear in an INT column.
    """
    if batch.col_type(val_col) is not ColType.INT:
        raise TypeError("group_sum_int requires an INT value column")
    keys = batch.column(key_col)
    vals = batch.column(val_col)
    out: dict = {}
    for key, val in zip(keys, vals):
        if key is NULL or val is NULL:
            continue
        out[key] = out.get(key, 0) + val
    return out


def sum_float(batch: TypedBatch, col: str):
    """Sum a FLOAT column: NULLs are skipped, NaN propagates (NaN != NULL).

    Returns NULL if no non-NULL value exists.
    """
    if batch.col_type(col) is not ColType.FLOAT:
        raise TypeError("sum_float requires a FLOAT column")
    total = 0.0
    seen = False
    for v in batch.column(col):
        if v is NULL:
            continue
        seen = True
        total += v  # NaN + anything stays NaN
    return total if seen else NULL
