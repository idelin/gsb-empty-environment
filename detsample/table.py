"""Typed batch table input.

Tables arrive as batches: a list of typed columns plus row tuples.
No external query engine is used; this is a minimal stdlib-only model.

Type rules:
- 'int'   : None or exact int (bool rejected; arbitrary precision kept)
- 'float' : None or float (NaN allowed and distinct from null)
- 'str'   : None or str
- 'bool'  : None or bool
- 'any'   : any supported scalar
"""

from __future__ import annotations

from typing import Any, Iterable, NamedTuple, Sequence

from .values import is_nan


class Column(NamedTuple):
    name: str
    type: str  # 'int' | 'float' | 'str' | 'bool' | 'any'


_VALID_TYPES = {"int", "float", "str", "bool", "any"}


def _check_value(col: Column, value: Any) -> bool:
    if value is None:
        return True  # null is allowed for every column type
    if col.type == "int":
        return isinstance(value, int) and not isinstance(value, bool)
    if col.type == "float":
        return isinstance(value, float)  # NaN is a valid float, != null
    if col.type == "str":
        return isinstance(value, str)
    if col.type == "bool":
        return isinstance(value, bool)
    return True  # 'any'


class Batch:
    """One typed batch of table rows."""

    def __init__(self, columns: Sequence[Column], rows: Iterable[Sequence[Any]]):
        self.columns: list[Column] = list(columns)
        names = [c.name for c in self.columns]
        if len(set(names)) != len(names):
            raise ValueError(f"duplicate column names: {names}")
        for col in self.columns:
            if col.type not in _VALID_TYPES:
                raise ValueError(f"unknown column type: {col.type!r}")
        self.rows: list[tuple] = []
        for i, row in enumerate(rows):
            row = tuple(row)
            if len(row) != len(self.columns):
                raise ValueError(
                    f"row {i} has {len(row)} values, expected {len(self.columns)}"
                )
            for col, value in zip(self.columns, row):
                if not _check_value(col, value):
                    raise TypeError(
                        f"row {i}: value {value!r} does not match column "
                        f"{col.name!r} of type {col.type!r}"
                    )
            self.rows.append(row)

    def column_index(self, name: str) -> int:
        for i, col in enumerate(self.columns):
            if col.name == name:
                return i
        raise KeyError(f"no such column: {name!r}")

    def __len__(self) -> int:
        return len(self.rows)
