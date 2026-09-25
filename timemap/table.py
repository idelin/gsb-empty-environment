"""合成偏移转换表：由分段仿射映射合成的 (断点, 速率, 偏移) 查询表。

表项按断点排序，点查询用二分定位，O(log n)；
所有量都是 Fraction，时间精确表示。
"""
from __future__ import annotations

from bisect import bisect_right

from .quantities import to_fraction


class OffsetTable:
    __slots__ = ("entries", "starts")

    def __init__(self, mapping):
        self.entries = tuple(
            (
                seg.domain.lo,
                seg.domain.hi,
                seg.domain.lo_closed,
                seg.domain.hi_closed,
                seg.rate,
                seg.offset,
                seg.seg_id,
            )
            for seg in mapping.segments
        )
        self.starts = [e[0] for e in self.entries]

    def convert(self, x):
        """把源轴上的点转换为逻辑值；不在任何段内返回 None。"""
        x = to_fraction(x)
        i = bisect_right(self.starts, x) - 1
        while i >= 0:
            lo, hi, lc, hc, rate, offset, _ = self.entries[i]
            if (x > lo or (x == lo and lc)) and (x < hi or (x == hi and hc)):
                return rate * x + offset
            i -= 1
        return None

    def __len__(self):
        return len(self.entries)

    def __repr__(self):
        rows = [
            f"  {'[' if lc else '('}{lo}, {hi}{']' if hc else ')'}"
            f" rate={rate} offset={offset} id={sid}"
            for lo, hi, lc, hc, rate, offset, sid in self.entries
        ]
        return "OffsetTable(\n" + "\n".join(rows) + "\n)"
