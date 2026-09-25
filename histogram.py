"""定宽直方图核心类型。

设计约定：
- 边界由调用方固定，桶为左闭右开区间 [b_i, b_{i+1})；
  落在桶右端的值进入下一个桶。
- 小于最小边界的值进入下溢桶，大于等于最大边界的值进入上溢桶。
- null（None）与 NaN 分别单独计数，且不计入分位数总体。
- 计数使用 Python int，秩计算使用 Fraction，全程整数精确，无浮点舍入。
- 分位数只返回值区间与误差说明，不伪称精确值。
"""

from __future__ import annotations

import math
from bisect import bisect_right
from dataclasses import dataclass
from fractions import Fraction
from typing import Iterable, Optional, Sequence, Tuple, Union

Number = Union[int, float]

NEG_INF = float("-inf")
POS_INF = float("inf")


class HistogramError(Exception):
    """直方图相关错误的基类。"""


class BoundaryMismatch(HistogramError):
    """两个直方图边界不同，不可直接合并。"""


class IncompatibleHistograms(HistogramError):
    """多个直方图在统计上不相容（不存在相容的非负整数分布）。"""


def _check_boundaries(boundaries: Sequence[Number]) -> Tuple[Number, ...]:
    b = tuple(boundaries)
    if len(b) < 2:
        raise ValueError("至少需要 2 个边界（即 1 个桶）")
    for x in b:
        if isinstance(x, bool) or not isinstance(x, (int, float)):
            raise TypeError(f"边界必须是 int/float，得到 {type(x).__name__}")
        if isinstance(x, float) and (math.isnan(x) or math.isinf(x)):
            raise ValueError("边界必须是有限值")
    for i in range(len(b) - 1):
        if not b[i] < b[i + 1]:
            raise ValueError("边界必须严格递增")
    return b


def parse_q(q: Union[int, float, str, Fraction]) -> Fraction:
    """把分位参数解析为精确的 Fraction，要求 0 <= q <= 1。"""
    if isinstance(q, bool):
        raise TypeError("q 不能是 bool")
    if isinstance(q, Fraction):
        f = q
    elif isinstance(q, int):
        f = Fraction(q)
    elif isinstance(q, float):
        if math.isnan(q) or math.isinf(q):
            raise ValueError("q 必须是有限值")
        f = Fraction(str(q))  # 按十进制字面量解释，避免二进制浮点噪声
    elif isinstance(q, str):
        f = Fraction(q)
    else:
        raise TypeError(f"不支持的 q 类型: {type(q).__name__}")
    if not (Fraction(0) <= f <= Fraction(1)):
        raise ValueError("q 必须在 [0, 1] 内")
    return f


def rank_of(q: Fraction, n: int) -> int:
    """逆 CDF 定义下的 1 基秩：r = max(1, ceil(q * n))，整数精确计算。"""
    if n <= 0:
        raise HistogramError("没有可用于分位数的非 null/NaN 样本")
    return max(1, math.ceil(q * n))


def _fmt(x: Number) -> str:
    if x == NEG_INF:
        return "-inf"
    if x == POS_INF:
        return "+inf"
    return repr(x)


@dataclass(frozen=True)
class QuantileInterval:
    """单一直方图的分位数结果：一个保证包含真实分位数的区间。"""

    q: Fraction
    rank: int
    n: int
    lo: Number
    hi: Number
    region: str
    explanation: str

    def __str__(self) -> str:
        return (f"QuantileInterval(q={self.q}, rank={self.rank}/{self.n}, "
                f"[{_fmt(self.lo)}, {_fmt(self.hi)}), region={self.region})")


class FixedHistogram:
    """边界固定的定宽直方图（左闭右开，含下溢/上溢/null/NaN 计数）。"""

    __slots__ = ("boundaries", "counts", "underflow", "overflow", "nulls", "nans")

    def __init__(self, boundaries: Sequence[Number],
                 counts: Optional[Sequence[int]] = None,
                 underflow: int = 0, overflow: int = 0,
                 nulls: int = 0, nans: int = 0):
        self.boundaries = _check_boundaries(boundaries)
        nb = len(self.boundaries) - 1
        if counts is None:
            self.counts = [0] * nb
        else:
            self.counts = [self._check_count(c) for c in counts]
            if len(self.counts) != nb:
                raise ValueError(f"counts 长度必须为 {nb}")
        self.underflow = self._check_count(underflow)
        self.overflow = self._check_count(overflow)
        self.nulls = self._check_count(nulls)
        self.nans = self._check_count(nans)

    @staticmethod
    def _check_count(c: int) -> int:
        if isinstance(c, bool) or not isinstance(c, int) or c < 0:
            raise ValueError(f"计数必须是非负 int，得到 {c!r}")
        return c

    @classmethod
    def from_values(cls, values: Iterable[Optional[Number]],
                    boundaries: Sequence[Number]) -> "FixedHistogram":
        h = cls(boundaries)
        for v in values:
            h.add(v)
        return h

    def add(self, value: Optional[Number]) -> None:
        if value is None:
            self.nulls += 1
            return
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise TypeError(f"不支持的值类型: {type(value).__name__}")
        if isinstance(value, float) and math.isnan(value):
            self.nans += 1
            return
        i = bisect_right(self.boundaries, value) - 1
        if i < 0:
            self.underflow += 1
        elif i >= len(self.counts):
            self.overflow += 1
        else:
            self.counts[i] += 1

    @property
    def total(self) -> int:
        """非 null/NaN 样本总数（分位数总体）。"""
        return self.underflow + sum(self.counts) + self.overflow

    def merge(self, other: "FixedHistogram") -> "FixedHistogram":
        """同边界直方图的精确合并（等价于直接处理合并后的样本）。"""
        if not isinstance(other, FixedHistogram):
            raise TypeError("只能与 FixedHistogram 合并")
        if self.boundaries != other.boundaries:
            raise BoundaryMismatch(
                "边界不同的直方图不可直接合并；跨边界请使用 "
                "constraints.union()（不同样本的并集）或 "
                "constraints.reconcile()（同一样本的调和）")
        return FixedHistogram(
            self.boundaries,
            [a + b for a, b in zip(self.counts, other.counts)],
            self.underflow + other.underflow,
            self.overflow + other.overflow,
            self.nulls + other.nulls,
            self.nans + other.nans)

    def quantile(self, q: Union[int, float, str, Fraction]) -> QuantileInterval:
        """返回保证包含真实 q 分位数的值区间，不伪称精确值。"""
        qf = parse_q(q)
        n = self.total
        r = rank_of(qf, n)
        cum = self.underflow
        if r <= cum:
            return self._interval(qf, r, n, NEG_INF, self.boundaries[0], "underflow")
        for i, c in enumerate(self.counts):
            cum += c
            if r <= cum:
                return self._interval(qf, r, n, self.boundaries[i],
                                      self.boundaries[i + 1], f"bucket[{i}]")
        return self._interval(qf, r, n, self.boundaries[-1], POS_INF, "overflow")

    def _interval(self, qf: Fraction, r: int, n: int,
                  lo: Number, hi: Number, region: str) -> QuantileInterval:
        expl = (
            f"秩 r=max(1,ceil(q*n))={r}，总体 n={n}（已排除 null={self.nulls}、"
            f"NaN={self.nans}）。第 {r} 个有序值落在 {region}，故真实分位数保证在 "
            f"[{_fmt(lo)}, {_fmt(hi)}) 内；桶内分布未知，区间宽度即误差上界，"
            f"无法在不作假定的情况下给出更精确的点值。")
        return QuantileInterval(qf, r, n, lo, hi, region, expl)

    def __eq__(self, other: object) -> bool:
        return (isinstance(other, FixedHistogram)
                and self.boundaries == other.boundaries
                and self.counts == other.counts
                and self.underflow == other.underflow
                and self.overflow == other.overflow
                and self.nulls == other.nulls
                and self.nans == other.nans)

    def __repr__(self) -> str:
        return (f"FixedHistogram(boundaries={self.boundaries}, counts={self.counts}, "
                f"underflow={self.underflow}, overflow={self.overflow}, "
                f"nulls={self.nulls}, nans={self.nans})")
