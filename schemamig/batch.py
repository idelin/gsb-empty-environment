"""Columnar batches: a table is a batch of typed columns plus a row count.

Values are validated against the column types on construction.  null
(None) and NaN (Decimal("NaN")) are distinct values; integers are exact
(arbitrary precision).
"""
from __future__ import annotations

from .schema import Schema
from .typesys import check_value, value_eq


class BatchError(Exception):
    pass


class Batch:
    def __init__(self, schema: Schema, columns):
        self.schema = schema
        expected = [c.cid for c in schema.columns]
        if set(columns) != set(expected):
            raise BatchError(
                f"batch columns {sorted(columns)} != schema columns {sorted(expected)}")
        cols = {cid: tuple(vals) for cid, vals in columns.items()}
        lengths = {len(v) for v in cols.values()}
        if len(lengths) > 1:
            raise BatchError("ragged batch: columns have different lengths")
        self.nrows = next(iter(lengths)) if lengths else 0
        for col in schema.columns:
            for i, value in enumerate(cols[col.cid]):
                if value is None:
                    if not col.nullable:
                        raise BatchError(
                            f"null in non-nullable column {col.name!r} row {i}")
                elif not check_value(col.type, value):
                    raise BatchError(
                        f"value {value!r} does not match type {col.type.value} "
                        f"in column {col.name!r} row {i}")
        self._columns = cols

    def column(self, cid: str):
        return self._columns[cid]

    def rows(self):
        order = [c.cid for c in self.schema.columns]
        return [tuple(self._columns[cid][i] for cid in order)
                for i in range(self.nrows)]

    @classmethod
    def from_rows(cls, schema: Schema, rows):
        rows = [tuple(r) for r in rows]
        cols = {c.cid: tuple(row[i] for row in rows)
                for i, c in enumerate(schema.columns)}
        return cls(schema, cols)

    def __eq__(self, other):
        if not isinstance(other, Batch):
            return NotImplemented
        if self.schema != other.schema or self.nrows != other.nrows:
            return False
        return all(
            value_eq(a, b)
            for cid in self._columns
            for a, b in zip(self._columns[cid], other._columns[cid]))

    def __repr__(self):
        return f"Batch({self.schema!r}, rows={self.rows()!r})"
