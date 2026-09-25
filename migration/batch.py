"""Typed column batches: the only table input format.

Null (None) and NaN are distinct values. Integer columns use exact
arbitrary-precision ints; NaN can only appear in FLOAT64 columns.
"""
from __future__ import annotations

import math
from dataclasses import dataclass
from enum import Enum
from typing import Any, Iterator


class Type(Enum):
    INT64 = "INT64"
    FLOAT64 = "FLOAT64"
    STRING = "STRING"
    BOOL = "BOOL"


@dataclass(frozen=True)
class Column:
    name: str
    type: Type
    values: tuple[Any, ...]

    def __init__(self, name: str, type: Type, values):
        object.__setattr__(self, "name", name)
        object.__setattr__(self, "type", type)
        object.__setattr__(self, "values", tuple(values))
        for v in self.values:
            self._check(v)

    def _check(self, v: Any) -> None:
        if v is None:
            return
        if self.type is Type.INT64:
            if isinstance(v, bool) or not isinstance(v, int):
                raise TypeError(f"column {self.name}: {v!r} is not INT64")
        elif self.type is Type.FLOAT64:
            if isinstance(v, bool) or not isinstance(v, (int, float)):
                raise TypeError(f"column {self.name}: {v!r} is not FLOAT64")
        elif self.type is Type.STRING:
            if not isinstance(v, str):
                raise TypeError(f"column {self.name}: {v!r} is not STRING")
        elif self.type is Type.BOOL:
            if not isinstance(v, bool):
                raise TypeError(f"column {self.name}: {v!r} is not BOOL")


class Batch:
    """A row batch of typed columns. No query engine involved."""

    def __init__(self, columns: list[Column]):
        if not columns:
            raise ValueError("batch needs at least one column")
        n = len(columns[0].values)
        for c in columns:
            if len(c.values) != n:
                raise ValueError("column length mismatch")
        names = [c.name for c in columns]
        if len(set(names)) != len(names):
            raise ValueError("duplicate column name")
        self._columns = {c.name: c for c in columns}
        self._n = n

    @property
    def num_rows(self) -> int:
        return self._n

    @property
    def column_names(self) -> list[str]:
        return list(self._columns)

    def column(self, name: str) -> Column:
        try:
            return self._columns[name]
        except KeyError:
            raise KeyError(f"no column {name!r}") from None

    def value(self, name: str, row: int) -> Any:
        return self.column(name).values[row]

    def is_null(self, name: str, row: int) -> bool:
        return self.value(name, row) is None

    def is_nan(self, name: str, row: int) -> bool:
        col = self.column(name)
        if col.type is not Type.FLOAT64:
            raise TypeError(f"is_nan only valid on FLOAT64, not {col.type.value}")
        v = col.values[row]
        return v is not None and math.isnan(v)

    def rows(self) -> Iterator[dict[str, Any]]:
        for i in range(self._n):
            yield {name: col.values[i] for name, col in self._columns.items()}
