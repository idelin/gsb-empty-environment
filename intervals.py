"""浮点区间运算：每步向外舍入，误差界随运算逐步（可加）累积。

用于熵接近阈值时的保守判定：真值落在 [lo, hi] 内，
区间整体不低于阈值 -> SATISFIED；整体低于阈值 -> VIOLATED；
阈值落在区间内（浮点精度无法区分）-> UNDECIDED。

说明：log 的误差边界采用每步向外扩展固定 ulp 的启发式工程界，
不构成严格数学证明。
"""
from __future__ import annotations

import math
from enum import Enum


class Decision(Enum):
    SATISFIED = "satisfied"
    VIOLATED = "violated"
    UNDECIDED = "undecided"


class Interval:
    """闭区间 [lo, hi]，所有运算向外舍入以保证包含真值。"""

    __slots__ = ("lo", "hi")

    def __init__(self, lo: float, hi: float | None = None):
        if hi is None:
            hi = lo
        lo, hi = float(lo), float(hi)
        if lo > hi:
            raise ValueError("区间下界大于上界")
        self.lo = lo
        self.hi = hi

    @classmethod
    def point(cls, x: float) -> "Interval":
        return cls(x, x)

    @property
    def width(self) -> float:
        return self.hi - self.lo

    @property
    def mid(self) -> float:
        return (self.lo + self.hi) / 2.0

    def __repr__(self) -> str:
        return f"Interval({self.lo!r}, {self.hi!r})"

    def __add__(self, other: "Interval") -> "Interval":
        return Interval(
            math.nextafter(self.lo + other.lo, -math.inf),
            math.nextafter(self.hi + other.hi, math.inf),
        )

    def __neg__(self) -> "Interval":
        return Interval(-self.hi, -self.lo)

    def __sub__(self, other: "Interval") -> "Interval":
        return self + (-other)

    def __mul__(self, other: "Interval") -> "Interval":
        products = (
            self.lo * other.lo,
            self.lo * other.hi,
            self.hi * other.lo,
            self.hi * other.hi,
        )
        return Interval(
            math.nextafter(min(products), -math.inf),
            math.nextafter(max(products), math.inf),
        )

    def __truediv__(self, other: "Interval") -> "Interval":
        if other.lo <= 0.0:
            raise ZeroDivisionError("区间除数必须为正")
        quots = (
            self.lo / other.lo,
            self.lo / other.hi,
            self.hi / other.lo,
            self.hi / other.hi,
        )
        return Interval(
            math.nextafter(min(quots), -math.inf),
            math.nextafter(max(quots), math.inf),
        )

    def log(self, ulp_margin: int = 4) -> "Interval":
        """自然对数；结果两端各向外扩展 ulp_margin 个 ulp 作为求值误差界。"""
        if self.lo <= 0.0:
            raise ValueError("非正区间无法取对数")
        lo, hi = math.log(self.lo), math.log(self.hi)
        for _ in range(ulp_margin):
            lo = math.nextafter(lo, -math.inf)
            hi = math.nextafter(hi, math.inf)
        return Interval(lo, hi)


def decide_at_least(interval: Interval, threshold: float) -> Decision:
    """判定区间所代表的真值是否 >= threshold（三值判定）。"""
    if interval.lo >= threshold:
        return Decision.SATISFIED
    if interval.hi < threshold:
        return Decision.VIOLATED
    return Decision.UNDECIDED
