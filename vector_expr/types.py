"""Logical types supported by the engine.

The type system is deliberately small and strict:

* ``INT64``   - exact integer arithmetic in [-2**63, 2**63 - 1];
                overflowing an operation is a per-row runtime error.
* ``FLOAT64`` - IEEE-754 doubles; NaN is a *value* and is distinct
                from null (missing).
* ``BOOL``    - three-valued under null: TRUE / FALSE / UNKNOWN(NULL).

There are no implicit conversions between types; mixing INT64 and
FLOAT64 in one operation is a compile-time error and must be resolved
with an explicit ``CAST``.
"""

from __future__ import annotations

from enum import Enum


class Type(Enum):
    INT64 = "INT64"
    FLOAT64 = "FLOAT64"
    BOOL = "BOOL"

    def __str__(self) -> str:  # pragma: no cover - cosmetic
        return self.value


INT64_MIN = -(2**63)
INT64_MAX = 2**63 - 1


def check_int64(value: int) -> bool:
    return INT64_MIN <= value <= INT64_MAX
