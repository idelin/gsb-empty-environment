"""Canonical typed-value encoding.

Encoding rules guarantee:
- null (None) and NaN are distinct values with distinct encodings;
- integers are encoded exactly (arbitrary precision, never via float);
- bool is distinct from int (bool is an int subclass in Python);
- encoding is injective: encode(a) == encode(b)  <=>  a and b are the
  same typed value.
"""

from __future__ import annotations

import math
from typing import Any

NULL_TAG = b"\x00"
BOOL_TAG = b"\x01"
INT_TAG = b"\x02"
FLOAT_TAG = b"\x03"
STR_TAG = b"\x04"


def is_nan(value: Any) -> bool:
    return isinstance(value, float) and math.isnan(value)


def encode_value(value: Any) -> bytes:
    """Encode a typed scalar value into canonical, comparable bytes."""
    if value is None:
        return NULL_TAG
    if isinstance(value, bool):
        return BOOL_TAG + (b"\x01" if value else b"\x00")
    if isinstance(value, int):
        # Exact decimal encoding; no float rounding ever happens here.
        return INT_TAG + str(value).encode("ascii")
    if isinstance(value, float):
        if math.isnan(value):
            return FLOAT_TAG + b"nan"
        if math.isinf(value):
            return FLOAT_TAG + (b"+inf" if value > 0 else b"-inf")
        # repr() round-trips doubles exactly in Python.
        return FLOAT_TAG + repr(value).encode("ascii")
    if isinstance(value, str):
        return STR_TAG + value.encode("utf-8")
    raise TypeError(f"unsupported value type: {type(value)!r}")


def values_equal(a: Any, b: Any) -> bool:
    """Typed equality where null == null and NaN == NaN, but null != NaN."""
    return encode_value(a) == encode_value(b)


def rows_equal(a: tuple, b: tuple) -> bool:
    return len(a) == len(b) and all(values_equal(x, y) for x, y in zip(a, b))
