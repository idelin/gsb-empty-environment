from __future__ import annotations

from numbers import Real
from typing import Any

from .model import Tri


def is_null(value: Any) -> bool:
    return value is None


def comparable(left: Any, right: Any) -> bool:
    if left is None or right is None:
        return True
    if isinstance(left, bool) or isinstance(right, bool):
        return isinstance(left, bool) and isinstance(right, bool)
    if isinstance(left, str) or isinstance(right, str):
        return isinstance(left, str) and isinstance(right, str)
    if isinstance(left, Real) and isinstance(right, Real):
        return True
    return type(left) is type(right)


def compare(left: Any, operator: str, right: Any) -> Tri:
    if left is None or right is None:
        return Tri.UNKNOWN
    if not comparable(left, right):
        return Tri.UNKNOWN
    if operator == "=":
        return Tri.TRUE if left == right else Tri.FALSE
    if operator == "<>":
        return Tri.TRUE if left != right else Tri.FALSE
    if operator == "<":
        return Tri.TRUE if left < right else Tri.FALSE
    if operator == "<=":
        return Tri.TRUE if left <= right else Tri.FALSE
    if operator == ">":
        return Tri.TRUE if left > right else Tri.FALSE
    if operator == ">=":
        return Tri.TRUE if left >= right else Tri.FALSE
    raise ValueError(f"unsupported operator: {operator}")


def not3(value: Tri) -> Tri:
    if value is Tri.TRUE:
        return Tri.FALSE
    if value is Tri.FALSE:
        return Tri.TRUE
    return Tri.UNKNOWN


def and3(left: Tri, right: Tri) -> Tri:
    if left is Tri.FALSE or right is Tri.FALSE:
        return Tri.FALSE
    if left is Tri.TRUE and right is Tri.TRUE:
        return Tri.TRUE
    return Tri.UNKNOWN


def or3(left: Tri, right: Tri) -> Tri:
    if left is Tri.TRUE or right is Tri.TRUE:
        return Tri.TRUE
    if left is Tri.FALSE and right is Tri.FALSE:
        return Tri.FALSE
    return Tri.UNKNOWN
