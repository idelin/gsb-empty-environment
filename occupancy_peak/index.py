"""增量扫描线索引：用端点差分 + 树状数组，避免每次更新重扫全部区间。"""
from __future__ import annotations

from bisect import bisect_left, bisect_right
from typing import List, Tuple


class SweepIndex:
    """维护半开区间集合的扫描线差分，支持增量更新与窗口峰值查询。

    原理：每个区间 [s, e) 只在两个端点产生计数变化（s 处 +1，e 处 -1）。
    新增 / 删除 / 更正一个区间只触碰这两个端点（O(log n) 级别），
    绝不重扫已有区间。

    树状数组（Fenwick）按坐标升序维护差分的前缀和：任意时刻 t 的在占
    用数 = 所有不晚于 t 的差分之和，O(log n) 可得。窗口峰值查询只需
    扫描落在窗口内部的变化点，复杂度 O(log n + k)，k 为窗口内端点数。

    坐标只增不删（净差分为 0 的坐标保留），保证树状数组位次稳定：
    已知坐标的端点更新是 O(log n) 点更新；出现全新时间戳时才插入坐标
    并重建树状数组（O(n)，均摊少见）。生产环境可换用带子树和的平衡
    树（如 AVL/红黑树扩展或 sortedcontainers）以获得稳定的 O(log n)。
    """

    def __init__(self) -> None:
        self._delta = {}   # time -> 该点的净计数变化
        self._times = []   # 所有出现过的坐标，升序，只增不删
        self._bit = [0]    # 树状数组，有效下标 1..len(_times)

    # ---- 更新：只触碰两个端点 ----

    def add_interval(self, s, e) -> None:
        self._bump(s, +1)
        self._bump(e, -1)

    def remove_interval(self, s, e) -> None:
        self._bump(s, -1)
        self._bump(e, +1)

    def _bump(self, t, v) -> None:
        i = bisect_left(self._times, t)
        if i == len(self._times) or self._times[i] != t:
            self._times.insert(i, t)
            self._delta[t] = v
            self._rebuild()
            return
        self._delta[t] += v
        self._add_bit(i, v)

    # ---- 查询 ----

    def level_at(self, t) -> int:
        """t 时刻的在占用数（含恰好在 t 生效 / 失效的端点）。"""
        return self._prefix(bisect_right(self._times, t))

    def peak_segments(self, qs, qe) -> Tuple[int, List[Tuple[float, float]]]:
        """窗口 [qs, qe) 内的峰值与所有达到峰值的最大连续时段（已裁剪到窗口）。"""
        level = self.level_at(qs)
        best = level
        seg_start = qs if level > 0 else None
        segments: List[Tuple[float, float]] = []
        lo = bisect_right(self._times, qs)   # <= qs 的差分已计入初始水位
        hi = bisect_left(self._times, qe)    # 只扫窗口内部的变化点
        for i in range(lo, hi):
            t = self._times[i]
            d = self._delta[t]
            if d == 0:
                continue
            new_level = level + d
            if seg_start is not None and level == best and new_level != best:
                segments.append((seg_start, t))
                seg_start = None
            if new_level > best:
                best = new_level
                segments.clear()
                seg_start = t
            elif new_level == best and seg_start is None and best > 0:
                seg_start = t
            level = new_level
        if seg_start is not None:
            segments.append((seg_start, qe))
        return best, segments

    # ---- 树状数组 ----

    def _prefix(self, i: int) -> int:
        s = 0
        while i > 0:
            s += self._bit[i]
            i -= i & -i
        return s

    def _add_bit(self, i: int, v: int) -> None:
        i += 1
        n = len(self._bit)
        while i < n:
            self._bit[i] += v
            i += i & -i

    def _rebuild(self) -> None:
        self._bit = [0] * (len(self._times) + 1)
        for i, t in enumerate(self._times):
            d = self._delta[t]
            if d:
                self._add_bit(i, d)
