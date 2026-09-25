"""带开闭端点的精确区间与区间集合。

端点开闭是语义的一部分：并、交、合并都严格保留开闭信息，
例如 [0,1) 与 [1,2) 相邻但不可合并（缺少点 1），
而 [0,1] 与 (1,2] 可以合并为 [0,2]。
"""
from __future__ import annotations

from dataclasses import dataclass

from .quantities import to_fraction


@dataclass(frozen=True)
class Interval:
    lo: object
    hi: object
    lo_closed: bool = True
    hi_closed: bool = False

    def __post_init__(self):
        object.__setattr__(self, "lo", to_fraction(self.lo))
        object.__setattr__(self, "hi", to_fraction(self.hi))
        if self.lo > self.hi:
            raise ValueError(f"empty interval: {self.lo} > {self.hi}")
        if self.lo == self.hi and not (self.lo_closed and self.hi_closed):
            raise ValueError("degenerate interval must be closed on both ends")

    @staticmethod
    def maybe(lo, hi, lo_closed=True, hi_closed=False):
        """构造区间；若为空则返回 None 而不是抛异常。"""
        lo, hi = to_fraction(lo), to_fraction(hi)
        if lo > hi or (lo == hi and not (lo_closed and hi_closed)):
            return None
        return Interval(lo, hi, lo_closed, hi_closed)

    @staticmethod
    def point(x):
        x = to_fraction(x)
        return Interval(x, x, True, True)

    @property
    def is_point(self):
        return self.lo == self.hi

    def contains(self, x) -> bool:
        x = to_fraction(x)
        if x < self.lo or x > self.hi:
            return False
        if x == self.lo and not self.lo_closed:
            return False
        if x == self.hi and not self.hi_closed:
            return False
        return True

    def intersect(self, other: "Interval"):
        if self.lo > other.lo:
            lo, lc = self.lo, self.lo_closed
        elif other.lo > self.lo:
            lo, lc = other.lo, other.lo_closed
        else:
            lo, lc = self.lo, self.lo_closed and other.lo_closed
        if self.hi < other.hi:
            hi, hc = self.hi, self.hi_closed
        elif other.hi < self.hi:
            hi, hc = other.hi, other.hi_closed
        else:
            hi, hc = self.hi, self.hi_closed and other.hi_closed
        return Interval.maybe(lo, hi, lc, hc)

    def mergeable(self, other: "Interval") -> bool:
        """self 在 other 左侧（lo 有序）时，两者之并是否仍是单个区间。"""
        if other.lo < self.hi:
            return True
        if other.lo == self.hi:
            return self.hi_closed or other.lo_closed
        return False

    def merge(self, other: "Interval") -> "Interval":
        if not self.mergeable(other):
            raise ValueError(f"cannot merge {self} with {other}")
        if other.hi > self.hi:
            hi, hc = other.hi, other.hi_closed
        elif other.hi < self.hi:
            hi, hc = self.hi, self.hi_closed
        else:
            hi, hc = self.hi, self.hi_closed or other.hi_closed
        return Interval(self.lo, hi, self.lo_closed, hc)

    def __repr__(self):
        left = "[" if self.lo_closed else "("
        right = "]" if self.hi_closed else ")"
        return f"{left}{self.lo}, {self.hi}{right}"


class IntervalSet:
    """有序、互不相交、不可再合并的区间集合（规范化存储）。"""

    __slots__ = ("intervals",)

    def __init__(self, intervals=()):
        items = sorted(
            (iv for iv in intervals if iv is not None),
            key=lambda iv: (iv.lo, not iv.lo_closed, iv.hi),
        )
        merged = []
        for iv in items:
            if merged and merged[-1].mergeable(iv):
                merged[-1] = merged[-1].merge(iv)
            else:
                merged.append(iv)
        self.intervals = tuple(merged)

    @staticmethod
    def of(*intervals):
        return IntervalSet(intervals)

    @staticmethod
    def point(x):
        return IntervalSet([Interval.point(x)])

    def union(self, other: "IntervalSet") -> "IntervalSet":
        return IntervalSet(self.intervals + other.intervals)

    def intersect(self, other: "IntervalSet") -> "IntervalSet":
        out = []
        for a in self.intervals:
            for b in other.intervals:
                r = a.intersect(b)
                if r is not None:
                    out.append(r)
        return IntervalSet(out)

    def contains(self, x) -> bool:
        return any(iv.contains(x) for iv in self.intervals)

    def __iter__(self):
        return iter(self.intervals)

    def __len__(self):
        return len(self.intervals)

    def __eq__(self, other):
        return isinstance(other, IntervalSet) and self.intervals == other.intervals

    def __hash__(self):
        return hash(self.intervals)

    def __repr__(self):
        return "IntervalSet(" + ", ".join(repr(iv) for iv in self.intervals) + ")"
