"""Typed record batches with explicit null/NaN semantics.

A *null* is ``None`` and means "no value".  ``NaN`` is a genuine float
value and is never treated as null.  Integer columns use Python ints, so
arithmetic on them is exact (arbitrary precision).
"""

from __future__ import annotations

import math

TYPES = ("int", "float", "str", "bool")


class SchemaError(ValueError):
    """Invalid schema definition or row data that violates the schema."""


def is_nan(value) -> bool:
    return isinstance(value, float) and math.isnan(value)


def _check_value(col_name, col_type, value):
    if value is None:
        return  # null is allowed in every column
    if col_type == "int":
        # bool is a subclass of int; reject it explicitly.
        if isinstance(value, bool) or not isinstance(value, int):
            raise SchemaError(
                f"column {col_name!r}: expected int, got {value!r}")
    elif col_type == "float":
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise SchemaError(
                f"column {col_name!r}: expected float, got {value!r}")
    elif col_type == "str":
        if not isinstance(value, str):
            raise SchemaError(
                f"column {col_name!r}: expected str, got {value!r}")
    elif col_type == "bool":
        if not isinstance(value, bool):
            raise SchemaError(
                f"column {col_name!r}: expected bool, got {value!r}")


class Schema:
    """An ordered list of ``(name, type)`` column definitions."""

    def __init__(self, columns):
        self.columns = [(name, typ) for name, typ in columns]
        names = [name for name, _ in self.columns]
        if len(set(names)) != len(names):
            raise SchemaError("duplicate column names")
        for name, typ in self.columns:
            if typ not in TYPES:
                raise SchemaError(f"column {name!r}: unknown type {typ!r}")

    @property
    def names(self):
        return [name for name, _ in self.columns]

    def index(self, name):
        for i, (col_name, _) in enumerate(self.columns):
            if col_name == name:
                return i
        raise SchemaError(f"unknown column {name!r}")

    def type_of(self, name):
        return self.columns[self.index(name)][1]

    def __eq__(self, other):
        return isinstance(other, Schema) and self.columns == other.columns

    def __repr__(self):
        return f"Schema({self.columns!r})"


class Batch:
    """A set of rows conforming to a schema; validated on construction."""

    def __init__(self, schema: Schema, rows):
        self.schema = schema
        self.rows = [tuple(row) for row in rows]
        width = len(schema.columns)
        for row in self.rows:
            if len(row) != width:
                raise SchemaError(
                    f"row has {len(row)} values, schema has {width} columns")
            for (col_name, col_type), value in zip(schema.columns, row):
                _check_value(col_name, col_type, value)

    def __len__(self):
        return len(self.rows)

    def __repr__(self):
        return f"Batch({self.schema!r}, {self.rows!r})"
