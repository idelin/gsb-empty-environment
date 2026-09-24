"""区间索引：居中区间树（centered interval tree）。

目的
----
朴素连接要做 n*m 的两两比较（全笛卡尔扫描）。本索引把一侧区间建成
平衡的居中树：

- 建树 O(n log n)（每层把区间分到左/中/右，共 O(log n) 层）；
- 点刺查 stab 与区间重叠查 overlaps 都是 O(log n + k)，
  k 为命中条数；每层用 bisect 在有序数组上截断，只产出真正命中
  的区间，绝不枚举不可能相交的对象。

约定
----
- 只索引半开区间 ``[lo, hi)`` 且 ``lo < hi``；零长/空长区间不入树，
  由调用方统计；
- 端点为同一数值域：全 int 时精确整数运算，全 float 时用浮点；
- payload 通常是全局行号；source_id 允许重复。
"""
from __future__ import annotations

import bisect
from dataclasses import dataclass
from typing import Any, Iterator, TypeVar, Generic

T = TypeVar("T")


@dataclass(frozen=True)
class Interval(Generic[T]):
    lo: Any
    hi: Any
    payload: T
    source_id: Any = None


@dataclass(frozen=True)
class QueryHit(Generic[T]):
    lo: Any
    hi: Any
    payload: T
    source_id: Any


class _Node:
    __slots__ = ("center", "by_lo", "by_hi", "lo_key", "hi_key", "left", "right")

    def __init__(self, center, by_lo, by_hi, left, right):
        self.center = center
        # 覆盖 center 的区间两种排序，配合 bisect 使用
        self.by_lo = by_lo
        self.by_hi = by_hi
        self.lo_key = [iv.lo for iv in by_lo]
        self.hi_key = [iv.hi for iv in by_hi]
        self.left = left
        self.right = right


class IntervalIndex:
    """``[lo, hi)`` 半开区间的居中区间树索引。"""

    def __init__(self, intervals: list[Interval]):
        self.size = len(intervals)
        self._root = self._build(intervals)

    @staticmethod
    def _build(ivs: list[Interval]) -> _Node | None:
        if not ivs:
            return None
        # 取 lo 的中位数作中心：至少有一个区间 lo==center 且 hi>center，
        # 它一定被本层 spanning 吸收，保证左右子树严格缩小（即便所有
        # 区间完全相同也不会无限递归）。
        center = sorted(iv.lo for iv in ivs)[len(ivs) // 2]

        left_items: list[Interval] = []
        right_items: list[Interval] = []
        spanning: list[Interval] = []
        for iv in ivs:
            if iv.hi <= center:
                left_items.append(iv)
            elif iv.lo > center:
                right_items.append(iv)
            else:  # iv.lo <= center < iv.hi
                spanning.append(iv)

        by_lo = tuple(sorted(spanning, key=lambda x: (x.lo, x.hi)))
        by_hi = tuple(sorted(spanning, key=lambda x: (x.hi, x.lo)))
        return _Node(
            center,
            by_lo,
            by_hi,
            IntervalIndex._build(left_items),
            IntervalIndex._build(right_items),
        )

    def stab(self, point: Any) -> Iterator[QueryHit]:
        """点刺查：产出所有 ``lo <= point < hi`` 的区间。

        5 不属于 [0,5)（半开），但属于 [5,8)。
        """
        return self._stab(self._root, point)

    @classmethod
    def _stab(cls, node: _Node | None, p: Any) -> Iterator[QueryHit]:
        if node is None:
            return
        if p < node.center:
            # 命中覆盖 center 的区间需 lo <= p（hi > center > p 已自动满足）。
            end = bisect.bisect_right(node.lo_key, p)
            for iv in node.by_lo[:end]:
                yield QueryHit(iv.lo, iv.hi, iv.payload, iv.source_id)
            yield from cls._stab(node.left, p)
        elif p > node.center:
            # 需 lo <= p 且 hi > p。用 by_hi 上 hi > p 截断，再检查 lo <= p。
            start = bisect.bisect_right(node.hi_key, p)
            for iv in node.by_hi[start:]:
                if iv.lo <= p:
                    yield QueryHit(iv.lo, iv.hi, iv.payload, iv.source_id)
            yield from cls._stab(node.right, p)
        else:  # p == center：覆盖 center 的区间全部包含 p，左右子树不含 p
            for iv in node.by_lo:
                yield QueryHit(iv.lo, iv.hi, iv.payload, iv.source_id)

    def overlaps(self, lo: Any, hi: Any) -> Iterator[QueryHit]:
        """与查询区间 ``[lo, hi)`` 有正长度交集的全部区间。

        条件 max(lo, iv.lo) < min(hi, iv.hi)；[0,5) 与 [5,8) 不命中，
        零长查询区间不命中任何区间。
        """
        if lo >= hi:
            return iter(())
        return self._overlaps(self._root, lo, hi)

    @classmethod
    def _overlaps(cls, node: _Node | None, lo: Any, hi: Any) -> Iterator[QueryHit]:
        if node is None:
            return
        if hi <= node.center:
            # 需 iv.lo < hi（iv.hi > center >= hi 已保证 iv.hi > lo 不一定，需检查）。
            end = bisect.bisect_left(node.lo_key, hi)
            for iv in node.by_lo[:end]:
                if iv.hi > lo:
                    yield QueryHit(iv.lo, iv.hi, iv.payload, iv.source_id)
            yield from cls._overlaps(node.left, lo, hi)
        elif lo > node.center:
            # 需 iv.hi > lo；再检查 iv.lo < hi。
            start = bisect.bisect_right(node.hi_key, lo)
            for iv in node.by_hi[start:]:
                if iv.lo < hi:
                    yield QueryHit(iv.lo, iv.hi, iv.payload, iv.source_id)
            yield from cls._overlaps(node.right, lo, hi)
        else:  # 查询区间覆盖 center：所有 spanning 区间都与它正长度相交
            for iv in node.by_lo:
                yield QueryHit(iv.lo, iv.hi, iv.payload, iv.source_id)
            yield from cls._overlaps(node.left, lo, hi)
            yield from cls._overlaps(node.right, lo, hi)
