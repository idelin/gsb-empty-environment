"""RecordBatch: typed columnar input for the vector engine."""
from __future__ import annotations

from .types import BOOL, FLOAT64, STRING


class RecordBatch:
    """A batch of rows stored column-wise, with a fixed typed schema.

    null is stored as None; NaN is an ordinary FLOAT64 value and is
    never treated as null.
    """

    def __init__(self, schema, columns):
        schema = dict(schema)
        columns = {name: list(values) for name, values in columns.items()}
        if set(schema) != set(columns):
            raise ValueError(
                f"schema/column mismatch: {sorted(schema)} vs {sorted(columns)}")
        lengths = {len(values) for values in columns.values()}
        if len(lengths) > 1:
            raise ValueError(f"ragged columns: lengths {sorted(lengths)}")
        self.schema = schema
        self.columns = columns
        self.num_rows = lengths.pop() if lengths else 0
        for name, typ in schema.items():
            self.columns[name] = [self._coerce(name, typ, value, row)
                                  for row, value in enumerate(self.columns[name])]

    @staticmethod
    def _coerce(name, typ, value, row):
        if value is None:
            return None
        where = f"column {name!r} row {row}"
        if typ == BOOL:
            if not isinstance(value, bool):
                raise TypeError(f"{where}: expected bool, got {type(value).__name__}")
            return value
        if typ.is_integer:
            if not isinstance(value, int) or isinstance(value, bool):
                raise TypeError(f"{where}: expected int, got {type(value).__name__}")
            if not (typ.min_value <= value <= typ.max_value):
                raise ValueError(f"{where}: {value} does not fit {typ}")
            return value
        if typ == FLOAT64:
            if not isinstance(value, (int, float)) or isinstance(value, bool):
                raise TypeError(f"{where}: expected float, got {type(value).__name__}")
            return float(value)
        if typ == STRING:
            if not isinstance(value, str):
                raise TypeError(f"{where}: expected str, got {type(value).__name__}")
            return value
        raise ValueError(f"column {name!r}: unsupported type {typ}")
