"""Typed batch table input.

Tables arrive as batches: a fixed column schema (name + type) plus row
tuples.  ``None`` is null and is allowed in every column; NaN is a float
value and is *not* null.  Integer columns are checked as exact Python ints
(bools excluded) so arbitrarily large integers stay exact.
"""

from __future__ import annotations

from dataclasses import dataclass

TYPE_NAMES = frozenset({"int", "float", "str", "bool", "bytes"})


@dataclass(frozen=True)
class Column:
    name: str
    type: str

    def __post_init__(self):
        if self.type not in TYPE_NAMES:
            raise ValueError(f"unknown column type: {self.type!r}")


def _check_value(value, col):
    if value is None:
        return None  # null is legal in every column
    t = col.type
    if t == "int":
        if isinstance(value, bool) or not isinstance(value, int):
            raise TypeError(f"column {col.name!r}: expected int, got {type(value).__name__}")
        return value
    if t == "bool":
        if not isinstance(value, bool):
            raise TypeError(f"column {col.name!r}: expected bool, got {type(value).__name__}")
        return value
    if t == "float":
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise TypeError(f"column {col.name!r}: expected float, got {type(value).__name__}")
        return float(value)
    if t == "str":
        if not isinstance(value, str):
            raise TypeError(f"column {col.name!r}: expected str, got {type(value).__name__}")
        return value
    if t == "bytes":
        if not isinstance(value, bytes):
            raise TypeError(f"column {col.name!r}: expected bytes, got {type(value).__name__}")
        return value
    raise AssertionError("unreachable")


class TableBatch:
    """One batch of a typed table: ``columns`` plus a list of row tuples."""

    def __init__(self, columns, rows):
        self.columns = tuple(columns)
        if not self.columns:
            raise ValueError("a batch needs at least one column")
        names = [c.name for c in self.columns]
        if len(set(names)) != len(names):
            raise ValueError("duplicate column names")
        self.rows = [self._validate_row(r) for r in rows]

    def _validate_row(self, row):
        row = tuple(row)
        if len(row) != len(self.columns):
            raise ValueError(
                f"row has {len(row)} values, schema has {len(self.columns)} columns"
            )
        return tuple(_check_value(v, c) for v, c in zip(row, self.columns))

    def column_index(self, name):
        for i, c in enumerate(self.columns):
            if c.name == name:
                return i
        raise KeyError(f"unknown column: {name!r}")

    def __len__(self):
        return len(self.rows)
