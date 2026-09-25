"""Typed data batches.

A Batch is a table chunk: a Schema plus one typed value vector per column.
Validation is strict (per-value type checks, nullability, row alignment).
Integer values are Python ints, so arithmetic on them is exact; no external
query engine is involved anywhere.
"""
from __future__ import annotations

from .schema import Schema
from .types import NULL, check_value, values_equal


class BatchError(Exception):
    pass


class Batch:
    def __init__(self, schema: Schema, data):
        expected = set(schema.names())
        if set(data.keys()) != expected:
            raise BatchError(
                f"batch columns {sorted(data.keys())} do not match "
                f"schema columns {sorted(expected)}"
            )
        columns = {}
        length = None
        for col in schema.columns:
            values = tuple(data[col.name])
            if length is None:
                length = len(values)
            elif len(values) != length:
                raise BatchError(
                    f"column {col.name!r} has {len(values)} rows, expected {length}"
                )
            for i, value in enumerate(values):
                if value is NULL:
                    if not col.nullable:
                        raise BatchError(
                            f"NULL at row {i} in non-nullable column {col.name!r}"
                        )
                elif not check_value(col.type, value):
                    raise BatchError(
                        f"value {value!r} at row {i} does not match type "
                        f"{col.type.value} of column {col.name!r}"
                    )
            columns[col.column_id] = values
        self.schema = schema
        self._data = columns
        self._row_count = length if length is not None else 0

    @property
    def row_count(self) -> int:
        return self._row_count

    def column_values(self, name):
        return self._data[self.schema.column(name).column_id]

    def row(self, index):
        return {c.name: self._data[c.column_id][index] for c in self.schema.columns}

    def rows(self):
        for i in range(self._row_count):
            yield self.row(i)

    def derive_column(self, column, fn):
        """Append a column computed row-wise. fn receives a row dict.
        Integer arithmetic inside fn stays exact (Python ints)."""
        if column.name in self.schema.names():
            raise BatchError(f"column {column.name!r} already exists")
        values = [fn(self.row(i)) for i in range(self._row_count)]
        new_schema = Schema(self.schema.columns + (column,), version=self.schema.version)
        data = {c.name: self.column_values(c.name) for c in self.schema.columns}
        data[column.name] = values
        return Batch(new_schema, data)

    def __eq__(self, other):
        if not isinstance(other, Batch) or self.schema != other.schema:
            return False
        for col in self.schema.columns:
            left = self._data[col.column_id]
            right = other._data[col.column_id]
            if len(left) != len(right):
                return False
            if any(not values_equal(a, b) for a, b in zip(left, right)):
                return False
        return True

    def __repr__(self):
        lines = [f"Batch({self.schema!r})"]
        for i in range(self._row_count):
            lines.append("  " + str(self.row(i)))
        return "\n".join(lines)
