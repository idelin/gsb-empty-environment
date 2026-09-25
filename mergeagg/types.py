"""Typed batch input model and value encoding.

A table arrives as a sequence of ``Batch`` objects.  Each batch declares its
column names and column types; every row is validated against those types.

``NULL`` is a singleton sentinel that is deliberately distinct from
``float("nan")``: ``NULL is not float("nan")`` and the two never compare
equal inside this package.
"""

from __future__ import annotations

import math
from enum import Enum
from typing import Any, Iterable, Sequence


class ColumnType(Enum):
    INT = "int"
    FLOAT = "float"
    STRING = "string"


class _Null:
    """Singleton sentinel for SQL-style NULL (distinct from NaN)."""

    _instance: "_Null | None" = None

    def __new__(cls) -> "_Null":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "NULL"

    def __str__(self) -> str:
        return "NULL"

    def __bool__(self) -> bool:
        return False

    def __reduce__(self):
        return (_Null, ())


NULL = _Null()


def is_null(value: Any) -> bool:
    return value is NULL


def is_nan(value: Any) -> bool:
    return isinstance(value, float) and math.isnan(value)


def check_value_type(value: Any, col_type: ColumnType, column: str) -> None:
    if value is NULL:
        return
    if isinstance(value, bool):
        raise TypeError(f"column {column!r}: bool is not a valid value")
    if col_type is ColumnType.INT:
        if not isinstance(value, int):
            raise TypeError(f"column {column!r}: expected int, got {type(value).__name__}")
    elif col_type is ColumnType.FLOAT:
        if not isinstance(value, (int, float)):
            raise TypeError(f"column {column!r}: expected float, got {type(value).__name__}")
    elif col_type is ColumnType.STRING:
        if not isinstance(value, str):
            raise TypeError(f"column {column!r}: expected str, got {type(value).__name__}")
    else:  # pragma: no cover - defensive
        raise ValueError(f"unknown column type {col_type!r}")


class Batch:
    """An immutable typed block of rows.

    Rows are validated eagerly so type errors surface at the source, not
    deep inside the merger.
    """

    __slots__ = ("columns", "types", "rows")

    def __init__(
        self,
        columns: Sequence[str],
        types: Sequence[ColumnType],
        rows: Iterable[Sequence[Any]],
    ) -> None:
        columns = tuple(columns)
        types = tuple(ColumnType(t) for t in types)
        if len(columns) != len(types):
            raise ValueError("columns and types must have the same length")
        if len(set(columns)) != len(columns):
            raise ValueError("duplicate column names")
        norm_rows = []
        for row in rows:
            row = tuple(row)
            if len(row) != len(columns):
                raise ValueError(
                    f"row has {len(row)} values, expected {len(columns)}"
                )
            for value, col_type, name in zip(row, types, columns):
                check_value_type(value, col_type, name)
            norm_rows.append(row)
        self.columns = columns
        self.types = types
        self.rows = norm_rows

    def __len__(self) -> int:
        return len(self.rows)

    def row(self, index: int) -> tuple:
        return self.rows[index]

    def column_index(self, name: str) -> int:
        return self.columns.index(name)

    def __repr__(self) -> str:
        return f"Batch(columns={self.columns!r}, rows={len(self.rows)})"


# ---------------------------------------------------------------------------
# Tagged value encoding for checkpoint serialization.
#
# Every value is encoded with an explicit type tag so that NULL, NaN, int,
# float and str survive a JSON round-trip without being conflated.  Integers
# are encoded as decimal strings so arbitrarily large ints stay exact even
# if the JSON is round-tripped through a parser with 64-bit or float limits.
# ---------------------------------------------------------------------------

def encode_value(value: Any) -> dict:
    if value is NULL:
        return {"t": "null"}
    if isinstance(value, bool):
        raise TypeError("bool values are not supported")
    if isinstance(value, int):
        return {"t": "int", "v": str(value)}
    if isinstance(value, float):
        if math.isnan(value):
            return {"t": "nan"}
        if math.isinf(value):
            return {"t": "inf", "v": 1 if value > 0 else -1}
        return {"t": "float", "v": value}
    if isinstance(value, str):
        return {"t": "str", "v": value}
    raise TypeError(f"unsupported value type: {type(value).__name__}")


def decode_value(data: dict) -> Any:
    tag = data["t"]
    if tag == "null":
        return NULL
    if tag == "int":
        return int(data["v"])
    if tag == "nan":
        return float("nan")
    if tag == "inf":
        return math.inf if data["v"] > 0 else -math.inf
    if tag == "float":
        return float(data["v"])
    if tag == "str":
        return data["v"]
    raise ValueError(f"unknown value tag: {tag!r}")
