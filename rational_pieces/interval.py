"""带开闭端点的区间；端点为 Fraction，None 表示 ±∞。"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


@dataclass(frozen=True)
class Interval:
    lo: Fraction | None
    hi: Fraction | None
    lo_closed: bool = False
    hi_closed: bool = False

    def __post_init__(self):
        lo = self.lo if self.lo is None or isinstance(self.lo, Fraction) else Fraction(self.lo)
        hi = self.hi if self.hi is None or isinstance(self.hi, Fraction) else Fraction(self.hi)
        object.__setattr__(self, 'lo', lo)
        object.__setattr__(self, 'hi', hi)
        if lo is None:
            object.__setattr__(self, 'lo_closed', False)
        if hi is None:
            object.__setattr__(self, 'hi_closed', False)
        if lo is not None and hi is not None and lo > hi:
            raise ValueError(f"区间下界大于上界: {lo} > {hi}")

    @staticmethod
    def closed(lo, hi) -> "Interval":
        return Interval(lo, hi, True, True)

    @staticmethod
    def open(lo, hi) -> "Interval":
        return Interval(lo, hi, False, False)

    @staticmethod
    def real_line() -> "Interval":
        return Interval(None, None)

    def is_empty(self) -> bool:
        if self.lo is None or self.hi is None:
            return False
        if self.lo > self.hi:
            return True
        return self.lo == self.hi and not (self.lo_closed and self.hi_closed)

    def contains(self, x) -> bool:
        x = x if isinstance(x, Fraction) else Fraction(x)
        if self.lo is not None:
            if x < self.lo or (x == self.lo and not self.lo_closed):
                return False
        if self.hi is not None:
            if x > self.hi or (x == self.hi and not self.hi_closed):
                return False
        return True

    def intersect(self, other: "Interval") -> "Interval | None":
        """交集；开闭性按“两侧皆闭才闭”精确保持。"""
        # 下界取较大者
        if self.lo is None:
            lo, lo_closed = other.lo, other.lo_closed
        elif other.lo is None:
            lo, lo_closed = self.lo, self.lo_closed
        elif self.lo > other.lo:
            lo, lo_closed = self.lo, self.lo_closed
        elif other.lo > self.lo:
            lo, lo_closed = other.lo, other.lo_closed
        else:
            lo, lo_closed = self.lo, self.lo_closed and other.lo_closed
        # 上界取较小者
        if self.hi is None:
            hi, hi_closed = other.hi, other.hi_closed
        elif other.hi is None:
            hi, hi_closed = self.hi, self.hi_closed
        elif self.hi < other.hi:
            hi, hi_closed = self.hi, self.hi_closed
        elif other.hi < self.hi:
            hi, hi_closed = other.hi, other.hi_closed
        else:
            hi, hi_closed = self.hi, self.hi_closed and other.hi_closed
        if lo is not None and hi is not None and lo > hi:
            return None
        iv = Interval(lo, hi, lo_closed, hi_closed)
        return None if iv.is_empty() else iv

    def __str__(self):
        lb = '[' if self.lo_closed else '('
        rb = ']' if self.hi_closed else ')'
        lo = '-∞' if self.lo is None else str(self.lo)
        hi = '+∞' if self.hi is None else str(self.hi)
        return f"{lb}{lo}, {hi}{rb}"

    __repr__ = __str__
