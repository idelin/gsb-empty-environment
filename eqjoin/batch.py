"""Typed record batches: the unit of table input."""
from __future__ import annotations

from .types import ColType, check_value


class RecordBatch:
    """A batch of rows plus its schema.

    schema: iterable of (column_name, ColType) pairs.
    rows:   iterable of tuples, one value per column, type-checked.
    """

    def __init__(self, schema, rows):
        self.schema = tuple((name, ColType(ct)) for name, ct in schema)
        names = [n for n, _ in self.schema]
        if len(set(names)) != len(names):
            raise ValueError(f"duplicate column names in schema: {names}")
        self.rows = []
        width = len(self.schema)
        for row in rows:
            row = tuple(row)
            if len(row) != width:
                raise ValueError(
                    f"row has {len(row)} values, schema has {width} columns"
                )
            for value, (name, coltype) in zip(row, self.schema):
                check_value(value, coltype, name)
            self.rows.append(row)

    def __len__(self) -> int:
        return len(self.rows)

    def column_index(self, name: str) -> int:
        for i, (n, _) in enumerate(self.schema):
            if n == name:
                return i
        raise KeyError(f"unknown column {name!r}")

    def __repr__(self) -> str:
        return f"RecordBatch(cols={len(self.schema)}, rows={len(self.rows)})"
