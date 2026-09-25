"""Column types and schema.

Types: INT (exact, arbitrary precision), FLOAT (IEEE-754 double, NaN is a
value distinct from null), STRING, BOOL. Null is represented by None and is
not a value of any type; NaN is a FLOAT value and is NOT null.
"""

from __future__ import annotations

import enum
import math


class TypeMismatchError(TypeError):
    """Raised when a predicate is built against an incompatible schema."""


class Type(enum.Enum):
    INT = "INT"
    FLOAT = "FLOAT"
    STRING = "STRING"
    BOOL = "BOOL"

    def check_value(self, value: object, where: str = "value") -> None:
        """Validate a non-null value against this type."""
        if value is None:
            return
        ok = {
            Type.INT: lambda v: isinstance(v, int) and not isinstance(v, bool),
            Type.FLOAT: lambda v: isinstance(v, float),
            Type.STRING: lambda v: isinstance(v, str),
            Type.BOOL: lambda v: isinstance(v, bool),
        }[self](value)
        if not ok:
            raise TypeMismatchError(
                f"{where}: {value!r} is not of type {self.value}"
            )


def is_nan(value: object) -> bool:
    return isinstance(value, float) and math.isnan(value)


def type_of_literal(value: object) -> Type:
    # bool must be checked before int (bool subclasses int).
    if isinstance(value, bool):
        return Type.BOOL
    if isinstance(value, int):
        return Type.INT
    if isinstance(value, float):
        return Type.FLOAT
    if isinstance(value, str):
        return Type.STRING
    raise TypeMismatchError(f"unsupported literal: {value!r}")


_NUMERIC = frozenset({Type.INT, Type.FLOAT})


def comparable(left: Type, right: Type) -> bool:
    if left in _NUMERIC and right in _NUMERIC:
        return True
    return left is right


class Schema:
    """Ordered mapping of column name -> Type."""

    def __init__(self, columns: dict[str, Type]) -> None:
        if not columns:
            raise ValueError("schema must have at least one column")
        for name, typ in columns.items():
            if not isinstance(typ, Type):
                raise TypeMismatchError(f"column {name!r}: bad type {typ!r}")
        self._columns = dict(columns)

    def type_of(self, name: str) -> Type:
        try:
            return self._columns[name]
        except KeyError:
            raise TypeMismatchError(f"unknown column: {name!r}") from None

    @property
    def names(self) -> tuple[str, ...]:
        return tuple(self._columns)

    def __repr__(self) -> str:
        cols = ", ".join(f"{n}: {t.value}" for n, t in self._columns.items())
        return f"Schema({cols})"
