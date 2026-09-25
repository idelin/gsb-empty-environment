"""Finite type domain, exact-widening rules and explicit conversions.

The supported type domain is finite: INT, DECIMAL, STRING, BOOL.
Only INT -> DECIMAL is an exact (implicit) widening.  Everything else
needs an explicit conversion op; lossy ones (DECIMAL -> INT) additionally
need a policy and keep per-value diagnostics.
"""
from __future__ import annotations

import enum
from dataclasses import dataclass
from decimal import Decimal, ROUND_DOWN, ROUND_FLOOR, ROUND_HALF_UP, InvalidOperation
from typing import Any, List


class Type(enum.Enum):
    INT = "int"
    DECIMAL = "decimal"
    STRING = "string"
    BOOL = "bool"


class Policy(enum.Enum):
    TRUNCATE = "truncate"
    FLOOR = "floor"
    ROUND_HALF_UP = "round_half_up"


_EXACT_WIDENING = frozenset({(Type.INT, Type.DECIMAL)})

_EXPLICIT_CONVERSIONS = frozenset({
    (Type.INT, Type.STRING),
    (Type.STRING, Type.INT),
    (Type.DECIMAL, Type.INT),
    (Type.DECIMAL, Type.STRING),
    (Type.STRING, Type.DECIMAL),
})

_LOSSY = frozenset({(Type.DECIMAL, Type.INT)})

_ROUNDING = {
    Policy.TRUNCATE: ROUND_DOWN,
    Policy.FLOOR: ROUND_FLOOR,
    Policy.ROUND_HALF_UP: ROUND_HALF_UP,
}


class ConversionError(Exception):
    """A value cannot be converted as requested."""


@dataclass(frozen=True)
class Diagnostic:
    column: str
    row: int
    old_value: Any
    new_value: Any
    reason: str


def is_exact_widening(src: Type, dst: Type) -> bool:
    return (src, dst) in _EXACT_WIDENING


def is_explicit_conversion(src: Type, dst: Type) -> bool:
    return (src, dst) in _EXPLICIT_CONVERSIONS


def is_lossy(src: Type, dst: Type) -> bool:
    return (src, dst) in _LOSSY


def is_nan(value: Any) -> bool:
    return isinstance(value, Decimal) and value.is_nan()


def value_eq(a: Any, b: Any) -> bool:
    """null/NaN-aware equality.  null and NaN are distinct values."""
    if a is None or b is None:
        return a is None and b is None
    if is_nan(a) or is_nan(b):
        return is_nan(a) and is_nan(b)
    if isinstance(a, bool) != isinstance(b, bool):
        return False
    return a == b


def check_value(typ: Type, value: Any) -> bool:
    if value is None:
        return True
    if typ is Type.INT:
        return isinstance(value, int) and not isinstance(value, bool)
    if typ is Type.DECIMAL:
        return isinstance(value, Decimal)
    if typ is Type.STRING:
        return isinstance(value, str)
    if typ is Type.BOOL:
        return isinstance(value, bool)
    raise AssertionError(f"unknown type {typ}")


def convert_value(value: Any, src: Type, dst: Type, policy: "Policy | None",
                  column: str, row: int, diagnostics: List[Diagnostic]) -> Any:
    if value is None or src is dst:
        return value
    pair = (src, dst)
    if pair == (Type.INT, Type.DECIMAL):
        return Decimal(value)  # exact widening, integers stay exact
    if pair == (Type.DECIMAL, Type.INT):
        if policy is None:
            raise ConversionError(
                f"column {column!r}: decimal->int requires an explicit policy")
        if is_nan(value):
            raise ConversionError(
                f"column {column!r} row {row}: NaN cannot become int")
        integral = value.to_integral_value(rounding=_ROUNDING[policy])
        result = int(integral)
        if value != integral:
            diagnostics.append(Diagnostic(
                column, row, value, result, "fractional part discarded"))
        return result
    if pair == (Type.INT, Type.STRING):
        return str(value)
    if pair == (Type.DECIMAL, Type.STRING):
        return str(value)
    if pair == (Type.STRING, Type.INT):
        try:
            return int(value)
        except ValueError:
            raise ConversionError(
                f"column {column!r} row {row}: {value!r} is not an int") from None
    if pair == (Type.STRING, Type.DECIMAL):
        try:
            return Decimal(value)
        except InvalidOperation:
            raise ConversionError(
                f"column {column!r} row {row}: {value!r} is not a decimal") from None
    raise ConversionError(f"unsupported conversion {src.value}->{dst.value}")
