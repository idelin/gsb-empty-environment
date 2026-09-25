"""Typed batch input model for the mini equi-join engine.

Defines:
- ``NULL``: a singleton sentinel for SQL-style NULL. It is distinct from
  ``float("nan")`` and never compares equal to anything (including itself)
  in join semantics.
- ``ColType``: the supported column types.
- ``Schema`` / ``Column``: typed column descriptions.
- ``Batch``: a typed block of rows; tables are consumed as iterators of
  batches so arbitrarily large inputs can stream through the joiner.
"""

from __future__ import annotations

import enum
from dataclasses import dataclass
from typing import Any, Iterator, Sequence


class _NullSentinel:
    """Singleton SQL NULL. Survives pickling (used by the spill files)."""

    _instance: "_NullSentinel | None" = None

    def __new__(cls) -> "_NullSentinel":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "NULL"

    def __bool__(self) -> bool:
        return False

    def __copy__(self) -> "_NullSentinel":
        return self

    def __deepcopy__(self, memo: dict) -> "_NullSentinel":
        return self


NULL = _NullSentinel()


class ColType(enum.Enum):
    INT = "int"
    FLOAT = "float"
    STR = "str"
    BOOL = "bool"

    def check(self, value: Any) -> bool:
        """Return True if ``value`` is acceptable for this column type.

        NULL is allowed in any column (nullable by default). NaN is a
        legitimate FLOAT value and is *not* NULL. Integers are exact
        (arbitrary precision) and BOOL is not silently accepted as INT.
        """
        if value is NULL:
            return True
        if self is ColType.INT:
            return isinstance(value, int) and not isinstance(value, bool)
        if self is ColType.FLOAT:
            return isinstance(value, float) or (
                isinstance(value, int) and not isinstance(value, bool)
            )
        if self is ColType.STR:
            return isinstance(value, str)
        if self is ColType.BOOL:
            return isinstance(value, bool)
        raise AssertionError(f"unknown type {self!r}")


@dataclass(frozen=True)
class Column:
    name: str
    col_type: ColType


class Schema:
    def __init__(self, columns: Sequence[Column | tuple[str, ColType]]):
        self.columns: tuple[Column, ...] = tuple(
            c if isinstance(c, Column) else Column(c[0], c[1]) for c in columns
        )
        names = [c.name for c in self.columns]
        if len(set(names)) != len(names):
            raise ValueError(f"duplicate column names: {names}")
        self._index = {c.name: i for i, c in enumerate(self.columns)}

    def __len__(self) -> int:
        return len(self.columns)

    def __eq__(self, other: object) -> bool:
        return isinstance(other, Schema) and self.columns == other.columns

    def __repr__(self) -> str:
        cols = ", ".join(f"{c.name}:{c.col_type.value}" for c in self.columns)
        return f"Schema({cols})"

    def column_index(self, name_or_index: str | int) -> int:
        if isinstance(name_or_index, int):
            if not 0 <= name_or_index < len(self.columns):
                raise KeyError(f"column index out of range: {name_or_index}")
            return name_or_index
        return self._index[name_or_index]

    def validate_row(self, row: Sequence[Any]) -> None:
        if len(row) != len(self.columns):
            raise ValueError(
                f"row has {len(row)} values, schema expects {len(self.columns)}"
            )
        for col, value in zip(self.columns, row):
            if not col.col_type.check(value):
                raise TypeError(
                    f"column {col.name!r} ({col.col_type.value}) got "
                    f"invalid value {value!r}"
                )


class Batch:
    """A typed block of rows. Tables are streams of batches."""

    def __init__(
        self,
        schema: Schema,
        rows: Sequence[Sequence[Any]],
        validate: bool = True,
    ):
        self.schema = schema
        self.rows: list[tuple[Any, ...]] = [tuple(r) for r in rows]
        if validate:
            for row in self.rows:
                schema.validate_row(row)

    def __iter__(self) -> Iterator[tuple[Any, ...]]:
        return iter(self.rows)

    def __len__(self) -> int:
        return len(self.rows)

    def __repr__(self) -> str:
        return f"Batch({self.schema!r}, rows={self.rows!r})"
