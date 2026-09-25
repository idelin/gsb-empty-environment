"""Columnar input: a table arrives as a batch of typed columns.

Nulls are represented as ``None`` slots inside the value list.  For
FLOAT64 columns, ``float('nan')`` is an ordinary value and compares
per IEEE-754; it is *not* null.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .types import Type, check_int64


@dataclass
class Column:
    """A typed vector. ``values[i] is None`` means row i is null."""

    type: Type
    values: list

    def __post_init__(self) -> None:
        for i, v in enumerate(self.values):
            if v is None:
                continue
            if self.type is Type.INT64:
                if isinstance(v, bool) or not isinstance(v, int):
                    raise ValueError(f"row {i}: {v!r} is not an INT64 value")
                if not check_int64(v):
                    raise ValueError(f"row {i}: {v!r} outside INT64 range")
            elif self.type is Type.FLOAT64:
                if isinstance(v, bool) or not isinstance(v, (int, float)):
                    raise ValueError(f"row {i}: {v!r} is not a FLOAT64 value")
                if isinstance(v, int):
                    self.values[i] = float(v)
            elif self.type is Type.BOOL:
                if not isinstance(v, bool):
                    raise ValueError(f"row {i}: {v!r} is not a BOOL value")
            else:  # pragma: no cover - defensive
                raise ValueError(f"unknown column type {self.type!r}")

    def __len__(self) -> int:
        return len(self.values)


@dataclass
class Batch:
    """A set of named columns of equal length."""

    columns: dict[str, Column] = field(default_factory=dict)

    def __post_init__(self) -> None:
        lengths = {len(c) for c in self.columns.values()}
        if len(lengths) > 1:
            raise ValueError(f"columns have different lengths: {lengths}")

    @property
    def num_rows(self) -> int:
        if not self.columns:
            return 0
        return len(next(iter(self.columns.values())))

    def column(self, name: str) -> Column:
        try:
            return self.columns[name]
        except KeyError:
            raise KeyError(f"batch has no column {name!r}") from None
