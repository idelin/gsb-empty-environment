"""Typed table batches.

Column types are enforced per value:
* INT accepts only real int instances, never bool.
* FLOAT accepts only float instances.
* STRING accepts only str instances.
* BOOL accepts only bool instances.
* None is SQL-style NULL and is accepted by every column.

Not-a-number is not a separate declared type: it is the floating-point value
float("nan") and is accepted only by a FLOAT column.
"""

from __future__ import annotations

from dataclasses import dataclass
from math import isnan
from typing import Any, Iterable, Mapping

INT = "INT"
FLOAT = "FLOAT"
STRING = "STRING"
BOOL = "BOOL"

_PY_TYPES = {
    INT: int,
    FLOAT: float,
    STRING: str,
    BOOL: bool,
}


@dataclass(frozen=True)
class TypedBatch:
    """A batch of rows plus an explicit schema."""

    columns: Mapping[str, str]
    rows: Iterable[Mapping[str, Any]]

    def __post_init__(self) -> None:
        columns = dict(self.columns)
        bad_types = sorted(set(columns.values()) - set(_PY_TYPES))
        if bad_types:
            raise TypeError(f"unsupported column types: {bad_types}")
        object.__setattr__(self, "columns", columns)
        object.__setattr__(self, "rows", list(self.rows))
        for row_number, row in enumerate(self.rows):
            if not isinstance(row, Mapping):
                raise TypeError(f"row {row_number} must be a mapping")
            for name, value in row.items():
                if name not in columns:
                    raise ValueError(f"row {row_number} has unknown column {name!r}")
                if value is None:
                    continue
                expected = columns[name]
                if expected == INT and (isinstance(value, bool) or not isinstance(value, int)):
                    raise TypeError(f"row {row_number}, column {name!r}: expected INT")
                if expected != INT and not isinstance(value, _PY_TYPES[expected]):
                    raise TypeError(f"row {row_number}, column {name!r}: expected {expected}")


def normalize_batches(batches: TypedBatch | Iterable[TypedBatch]) -> list[TypedBatch]:
    """Normalize one batch or an iterable of batches to a list."""

    if isinstance(batches, TypedBatch):
        return [batches]
    result = list(batches)
    if not all(isinstance(batch, TypedBatch) for batch in result):
        raise TypeError("all inputs must be TypedBatch instances")
    return result


def is_null(value: object) -> bool:
    """Return whether value is SQL NULL."""

    return value is None


def is_nan(value: object) -> bool:
    """Return whether value is floating-point NaN.

    Unlike NULL, NaN is a value. It therefore cannot equal itself or join on
    equality, but it can still be retained as a source ID.
    """

    return isinstance(value, float) and isnan(value)
