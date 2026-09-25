"""精确时间量：区分墙钟时间、逻辑时间点与持续时长。

三种量的底层都是 fractions.Fraction，保证有理数精确运算；
类型系统禁止混用不同含义的量（例如 墙钟 + 逻辑时间点）。
"""
from __future__ import annotations

from fractions import Fraction


def to_fraction(value) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("bool is not a valid time quantity")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(f"cannot convert {type(value).__name__} to Fraction")


class Duration:
    """持续时长（可正可负的精确有理数）。"""

    __slots__ = ("value",)

    def __init__(self, value=0):
        self.value = to_fraction(value)

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self.value + other.value)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self.value - other.value)
        return NotImplemented

    def __mul__(self, factor):
        if isinstance(factor, (int, Fraction)):
            return Duration(self.value * factor)
        return NotImplemented

    __rmul__ = __mul__

    def __neg__(self):
        return Duration(-self.value)

    def __eq__(self, other):
        return isinstance(other, Duration) and self.value == other.value

    def __lt__(self, other):
        if isinstance(other, Duration):
            return self.value < other.value
        return NotImplemented

    def __le__(self, other):
        if isinstance(other, Duration):
            return self.value <= other.value
        return NotImplemented

    def __hash__(self):
        return hash((Duration, self.value))

    def __repr__(self):
        return f"Duration({self.value})"


class _Instant:
    """时间轴上的点；只允许 点-点=时长、点±时长=点。"""

    __slots__ = ("value",)

    def __init__(self, value=0):
        self.value = to_fraction(value)

    def __add__(self, other):
        if isinstance(other, Duration):
            return type(self)(self.value + other.value)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return type(self)(self.value - other.value)
        if isinstance(other, type(self)):
            return Duration(self.value - other.value)
        return NotImplemented

    def __eq__(self, other):
        return isinstance(other, type(self)) and self.value == other.value

    def __lt__(self, other):
        if isinstance(other, type(self)):
            return self.value < other.value
        return NotImplemented

    def __le__(self, other):
        if isinstance(other, type(self)):
            return self.value <= other.value
        return NotImplemented

    def __hash__(self):
        return hash((type(self), self.value))

    def __repr__(self):
        return f"{type(self).__name__}({self.value})"


class WallClock(_Instant):
    """墙钟时间：真实/物理时间轴上的点（映射的源）。"""


class TimePoint(_Instant):
    """逻辑时间点：映射后的连续逻辑时间轴上的点（映射的像）。"""
