"""Column types and the NULL sentinel.

NULL is a singleton distinct from any value, including float('nan').
NaN is an ordinary FLOAT value that (per IEEE) never equals itself,
so NaN keys never match -- but NaN is NOT NULL and is preserved as a
regular value in output rows.
"""
from __future__ import annotations

import enum
import math


class _Null:
    """Singleton NULL sentinel. Distinct from None and from NaN."""

    _instance = None

    def __new__(cls) -> "_Null":
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self) -> str:
        return "NULL"

    def __bool__(self) -> bool:
        return False

    def __copy__(self) -> "_Null":
        return self

    def __deepcopy__(self, memo) -> "_Null":
        return self

    def __reduce__(self):
        # Keep the singleton identity across pickle (spill files).
        return (_null_singleton, ())


def _null_singleton() -> _Null:
    return NULL


NULL = _Null()


def is_null(value) -> bool:
    return value is NULL


def is_nan(value) -> bool:
    return isinstance(value, float) and math.isnan(value)


class ColType(enum.Enum):
    INT = "int"      # Python int, arbitrary precision, exact
    FLOAT = "float"  # Python float; NaN allowed and distinct from NULL
    TEXT = "text"    # Python str
    BOOL = "bool"    # Python bool


def check_value(value, coltype: ColType, column: str = "?") -> None:
    """Validate one value against its declared column type.

    NULL is allowed in every column (nullable by default).
    INT is exact: bools and floats are rejected.
    """
    if value is NULL:
        return
    ok = False
    if coltype is ColType.INT:
        ok = isinstance(value, int) and not isinstance(value, bool)
    elif coltype is ColType.FLOAT:
        ok = isinstance(value, float)
    elif coltype is ColType.TEXT:
        ok = isinstance(value, str)
    elif coltype is ColType.BOOL:
        ok = isinstance(value, bool)
    if not ok:
        raise TypeError(
            f"column {column!r} expects {coltype.value}, "
            f"got {type(value).__name__} ({value!r})"
        )
