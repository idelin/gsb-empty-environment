"""Type system: a small finite type domain with explicit widening rules.

- NULL is a singleton sentinel, distinct from float NaN.
- Implicit (lossless) widening: INT -> DECIMAL only.
- Explicit (potentially lossy) conversion: DECIMAL -> INT, requires a policy.
"""
from __future__ import annotations

import enum
import math
from decimal import ROUND_HALF_UP, Decimal


class TypeTag(enum.Enum):
    INT = "int"
    DECIMAL = "decimal"
    FLOAT = "float"
    STRING = "string"
    BOOL = "bool"


class _Null:
    """Singleton SQL-style NULL. Never equal to NaN or any value."""

    _instance = None

    def __new__(cls):
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self):
        return "NULL"

    def __str__(self):
        return "NULL"


NULL = _Null()


class RoundingPolicy(enum.Enum):
    """Explicit policies for lossy DECIMAL -> INT conversion."""

    TRUNCATE = "truncate"
    ROUND_HALF_UP = "round_half_up"
    REJECT_ON_LOSS = "reject_on_loss"


IMPLICIT_WIDENING = frozenset({(TypeTag.INT, TypeTag.DECIMAL)})
EXPLICIT_CONVERSIONS = frozenset({(TypeTag.DECIMAL, TypeTag.INT)})


def is_implicit_widening(src: TypeTag, dst: TypeTag) -> bool:
    return src == dst or (src, dst) in IMPLICIT_WIDENING


def is_explicit_conversion(src: TypeTag, dst: TypeTag) -> bool:
    return (src, dst) in EXPLICIT_CONVERSIONS


def check_value(tag: TypeTag, value) -> bool:
    if value is NULL:
        return True
    if tag is TypeTag.INT:
        return isinstance(value, int) and not isinstance(value, bool)
    if tag is TypeTag.DECIMAL:
        return isinstance(value, Decimal)
    if tag is TypeTag.FLOAT:
        return isinstance(value, float)
    if tag is TypeTag.STRING:
        return isinstance(value, str)
    if tag is TypeTag.BOOL:
        return isinstance(value, bool)
    return False


def values_equal(left, right) -> bool:
    """NULL/NaN-aware, type-strict value equality."""
    if left is NULL or right is NULL:
        return left is NULL and right is NULL
    if isinstance(left, float) and isinstance(right, float):
        if math.isnan(left) and math.isnan(right):
            return True
    if type(left) is not type(right):
        return False
    return bool(left == right)


def widen_value(src: TypeTag, dst: TypeTag, value):
    """Lossless implicit widening. INT -> DECIMAL is exact."""
    if value is NULL or src == dst:
        return value
    if (src, dst) == (TypeTag.INT, TypeTag.DECIMAL):
        return Decimal(value)
    raise TypeError(f"no implicit widening from {src.value} to {dst.value}")


def convert_value(src: TypeTag, dst: TypeTag, value, policy: RoundingPolicy):
    """Explicit conversion. Returns (converted, lossy). Raises ValueError on
    rejected or impossible conversions."""
    if value is NULL:
        return NULL, False
    if (src, dst) == (TypeTag.DECIMAL, TypeTag.INT):
        if value.is_nan() or value.is_infinite():
            raise ValueError(f"cannot convert {value!r} to int")
        if value == value.to_integral_value():
            return int(value), False
        if policy is RoundingPolicy.REJECT_ON_LOSS:
            raise ValueError(f"lossy conversion of {value!r} to int rejected")
        if policy is RoundingPolicy.TRUNCATE:
            return int(value), True
        if policy is RoundingPolicy.ROUND_HALF_UP:
            return int(value.quantize(Decimal("1"), rounding=ROUND_HALF_UP)), True
        raise ValueError(f"unknown policy {policy!r}")
    raise ValueError(f"no explicit conversion from {src.value} to {dst.value}")
