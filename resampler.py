"""可更正的重采样器：不规则采样 -> 固定时间网格。

特性：
- 两种模式：右连续阶梯（step）与线性插值（linear）。
- 显式断点与超过上限的缺测间隔禁止跨越插值。
- 每个输出网格点携带：所用原始采样 ID、模式、有效性（及无效原因）。
- 迟到采样、撤回、断点增删都会产生修订（Revision）。
- 时间一律用 Fraction 精确表示，网格点时刻严格等于 t0 + k*step；
  插值在 Fraction 上精确计算，输出值统一量化到 VALUE_QUANT，
  舍入规则固定为 ROUND_HALF_EVEN。
"""

from __future__ import annotations

import math
from bisect import bisect_left, bisect_right, insort
from dataclasses import dataclass
from decimal import Decimal, ROUND_HALF_EVEN, localcontext
from fractions import Fraction
from typing import Dict, List, Optional, Tuple

MODE_STEP = "step"      # 右连续阶梯：t 处取最后一个采样时刻 <= t 的样本
MODE_LINEAR = "linear"  # 线性：t 处取两侧最近样本线性插值

# 固定的输出精度与舍入规则
VALUE_QUANT = Decimal("0.000001")
VALUE_ROUNDING = ROUND_HALF_EVEN

# 无效原因
NO_SAMPLE = "no-sample"      # 一侧（或两侧）没有可用样本
GAP = "gap-exceeded"         # 缺测间隔超过上限
BREAKPOINT = "breakpoint"    # 插值区间跨越显式断点


def to_fraction(x) -> Fraction:
    """把 int/float/str/Decimal/Fraction 统一成精确的 Fraction。

    float 与 Decimal 按其十进制字面量解释（Fraction(str(x))），
    保证规则确定、可复现，不引入二进制浮点误差。
    """
    if isinstance(x, Fraction):
        return x
    if isinstance(x, (float, Decimal)):
        return Fraction(str(x))
    return Fraction(x)


def quantize_value(fr: Fraction) -> Decimal:
    """固定的输出量化：1e-6，ROUND_HALF_EVEN。"""
    with localcontext() as ctx:
        ctx.prec = 50
        d = Decimal(fr.numerator) / Decimal(fr.denominator)
        return d.quantize(VALUE_QUANT, rounding=VALUE_ROUNDING)


def _effective_entry(entries: List[Tuple[int, Fraction]]) -> Tuple[int, Fraction]:
    """同一时刻的多个样本中，ID 最大（最晚到达）者生效。"""
    return max(entries, key=lambda e: e[0])


def _compute_point(mode, times, by_time, breaks, max_gap, t):
    """核心判定（纯函数，批量参考与增量引擎共用）。

    times:   有序的有效采样时刻列表（每个时刻至少有一个存活样本）
    by_time: 时刻 -> [(sample_id, value), ...]
    breaks:  有序断点时刻列表
    返回 (valid, value_fraction_or_None, source_ids, reason_or_None)
    """
    if mode == MODE_STEP:
        i = bisect_right(times, t) - 1
        if i < 0:
            return False, None, (), NO_SAMPLE
        s = times[i]
        if max_gap is not None and t - s > max_gap:
            return False, None, (), GAP
        # 断点 b 满足 s < b <= t 时，保持值不可跨越断点
        j = bisect_right(breaks, s)
        if j < len(breaks) and breaks[j] <= t:
            return False, None, (), BREAKPOINT
        sid, val = _effective_entry(by_time[s])
        return True, val, (sid,), None

    # linear
    i = bisect_left(times, t)
    if i < len(times) and times[i] == t:
        # 网格点恰有样本：直接观测，不涉及跨断点/跨缺测插值
        sid, val = _effective_entry(by_time[t])
        return True, val, (sid,), None
    if i == 0 or i == len(times):
        return False, None, (), NO_SAMPLE
    s1, s2 = times[i - 1], times[i]
    if max_gap is not None and s2 - s1 > max_gap:
        return False, None, (), GAP
    # 断点严格落在插值区间 (s1, s2) 内 => 禁止跨越
    j = bisect_right(breaks, s1)
    if j < len(breaks) and breaks[j] < s2:
        return False, None, (), BREAKPOINT
    id1, v1 = _effective_entry(by_time[s1])
    id2, v2 = _effective_entry(by_time[s2])
    val = v1 + (v2 - v1) * (t - s1) / (s2 - s1)
    return True, val, (id1, id2), None


@dataclass(frozen=True)
class GridPoint:
    index: int                      # 网格下标 k
    time: Fraction                  # 精确网格时刻 t0 + k*step
    mode: str                       # "step" | "linear"
    valid: bool
    value: Optional[Decimal]        # 量化后的值；无效时为 None
    source_ids: Tuple[int, ...]     # 所用的原始采样 ID
    reason: Optional[str] = None    # 无效原因


@dataclass(frozen=True)
class Revision:
    index: int
    time: Fraction
    old: GridPoint
    new: GridPoint


def _make_point(index, t, mode, computed) -> GridPoint:
    valid, val, ids, reason = computed
    return GridPoint(
        index=index,
        time=t,
        mode=mode,
        valid=valid,
        value=quantize_value(val) if valid else None,
        source_ids=tuple(ids),
        reason=reason,
    )


class Resampler:
    """维护样本/断点状态，按网格输出，并对每次变更产生修订。"""

    def __init__(self, t0, step, count, mode=MODE_LINEAR, max_gap=None):
        self.t0 = to_fraction(t0)
        self.step = to_fraction(step)
        if self.step <= 0:
            raise ValueError("step must be positive")
        self.count = int(count)
        if self.count < 0:
            raise ValueError("count must be >= 0")
        if mode not in (MODE_STEP, MODE_LINEAR):
            raise ValueError("mode must be 'step' or 'linear'")
        self.mode = mode
        self.max_gap = None if max_gap is None else to_fraction(max_gap)

        self._times: List[Fraction] = []                 # 有序有效采样时刻
        self._by_time: Dict[Fraction, List[Tuple[int, Fraction]]] = {}
        self._by_id: Dict[int, Tuple[Fraction, Fraction]] = {}
        self._breaks: List[Fraction] = []                # 有序断点
        self._next_id = 1
        self._watermark: Optional[Fraction] = None       # 封闭水位线
        self._outputs: List[GridPoint] = [
            self._point_at(k) for k in range(self.count)
        ]

    # ---------- 查询 ----------

    def grid_time(self, k: int) -> Fraction:
        """精确网格时刻：不做任何浮点运算。"""
        return self.t0 + k * self.step

    def query(self, k: int) -> GridPoint:
        return self._outputs[k]

    def grid(self) -> List[GridPoint]:
        return list(self._outputs)

    def compute_range(self, k0: int, k1: int) -> List[GridPoint]:
        """从当前状态重算 [k0, k1)，供分块对照使用。"""
        if not (0 <= k0 <= k1 <= self.count):
            raise ValueError("bad range")
        return [self._point_at(k) for k in range(k0, k1)]

    # ---------- 摄入 / 撤回 / 断点 ----------

    def ingest(self, t, value) -> Tuple[int, List[Revision]]:
        """摄入样本（允许迟到），返回 (sample_id, revisions)。"""
        t = to_fraction(t)
        v = to_fraction(value)
        self._check_open(t)
        sid = self._next_id
        self._next_id += 1
        lo, hi = self._neighbors_around(t)
        if t in self._by_time:
            self._by_time[t].append((sid, v))
        else:
            self._by_time[t] = [(sid, v)]
            insort(self._times, t)
        self._by_id[sid] = (t, v)
        return sid, self._recompute_span(lo, hi, t)

    def retract(self, sample_id: int) -> List[Revision]:
        """撤回样本。被遮蔽的同期样本可能复活；受影响输出转为未知或改值。"""
        if sample_id not in self._by_id:
            raise KeyError(f"unknown or released sample id: {sample_id}")
        t, _ = self._by_id[sample_id]
        entries = self._by_time[t]
        was_effective = _effective_entry(entries)[0] == sample_id
        lo, hi = self._neighbors_around(t)
        self._by_time[t] = [e for e in entries if e[0] != sample_id]
        if not self._by_time[t]:
            del self._by_time[t]
            self._times.pop(bisect_left(self._times, t))
        del self._by_id[sample_id]
        if not was_effective:
            return []
        return self._recompute_span(lo, hi, t)

    def add_breakpoint(self, t) -> List[Revision]:
        t = to_fraction(t)
        self._check_open(t)
        i = bisect_left(self._breaks, t)
        if i < len(self._breaks) and self._breaks[i] == t:
            return []
        lo, hi = self._neighbors_around(t)
        self._breaks.insert(i, t)
        return self._recompute_span(lo, hi, t)

    def remove_breakpoint(self, t) -> List[Revision]:
        t = to_fraction(t)
        i = bisect_left(self._breaks, t)
        if i >= len(self._breaks) or self._breaks[i] != t:
            raise KeyError(f"no breakpoint at {t}")
        lo, hi = self._neighbors_around(t)
        self._breaks.pop(i)
        return self._recompute_span(lo, hi, t)

    # ---------- 封闭与释放 ----------

    def seal(self, watermark) -> List[GridPoint]:
        """封闭 watermark 之前的区间，释放不再需要的样本与断点。

        规则：
        - 保留 watermark 前最后一个有时刻的样本（线性左邻 / 阶梯持有的来源），
          其余 t < watermark 的样本（含被遮蔽者）全部释放；
        - 保留 watermark 前最后一个断点（与边界样本共同决定跨越判定），
          其余 t < watermark 的断点全部释放；
        - 之后拒绝一切 t < watermark 的摄入/断点，被释放 ID 的撤回报 KeyError。
        返回已封闭的网格点（t < watermark），调用方可归档。
        """
        w = to_fraction(watermark)
        if self._watermark is not None and w < self._watermark:
            raise ValueError("watermark cannot move backward")
        self._watermark = w

        older = [t for t in self._times if t < w]
        boundary = older[-1] if older else None
        for t in older:
            if t == boundary:
                continue
            for sid, _ in self._by_time[t]:
                del self._by_id[sid]
            del self._by_time[t]
        self._times = ([boundary] if boundary is not None else []) + self._times[len(older):]

        i = bisect_left(self._breaks, w)
        last_break = self._breaks[i - 1] if i > 0 else None
        self._breaks = ([last_break] if last_break is not None else []) + self._breaks[i:]

        return [p for p in self._outputs if p.time < w]

    # ---------- 内部 ----------

    def _check_open(self, t: Fraction):
        if self._watermark is not None and t < self._watermark:
            raise ValueError(f"time {t} is before sealed watermark {self._watermark}")

    def _point_at(self, k: int) -> GridPoint:
        computed = _compute_point(
            self.mode, self._times, self._by_time, self._breaks,
            self.max_gap, self.grid_time(k),
        )
        return _make_point(k, self.grid_time(k), self.mode, computed)

    def _neighbors_around(self, t: Fraction):
        """有效采样时刻中 t 的左右邻居（均不含 t 本身）。"""
        i = bisect_left(self._times, t)
        lo = self._times[i - 1] if i > 0 else None
        j = bisect_right(self._times, t)
        hi = self._times[j] if j < len(self._times) else None
        return lo, hi

    def _k_first_gt(self, x: Optional[Fraction]) -> int:
        if x is None:
            return 0
        return math.floor((x - self.t0) / self.step) + 1

    def _k_first_ge(self, x: Optional[Fraction]) -> int:
        if x is None:
            return 0
        return math.ceil((x - self.t0) / self.step)

    def _recompute_span(self, lo, hi, t) -> List[Revision]:
        """按模式计算受影响网格下标范围，重算并 diff 出修订。"""
        if self.mode == MODE_LINEAR:
            # 受影响点严格位于左右邻居之间（邻居处为直接观测，不受影响）
            k0 = self._k_first_gt(lo)
            k1 = self._k_first_ge(hi) if hi is not None else self.count
        else:
            # 阶梯：t 起、到下一个样本时刻为止
            k0 = self._k_first_ge(t)
            k1 = self._k_first_ge(hi) if hi is not None else self.count
        return self._recompute_range(k0, k1)

    def _recompute_range(self, k0: int, k1: int) -> List[Revision]:
        revs = []
        for k in range(max(0, k0), min(self.count, k1)):
            new = self._point_at(k)
            old = self._outputs[k]
            if new != old:
                self._outputs[k] = new
                revs.append(Revision(index=k, time=new.time, old=old, new=new))
        return revs


def compute_reference(mode, samples, breakpoints, t0, step, count, max_gap=None) -> List[GridPoint]:
    """整段批量参考实现：给定存活样本与断点，一次性算出整个网格。

    samples:     可迭代的 (sample_id, t, value)
    breakpoints: 可迭代的 t
    """
    t0f, stepf = to_fraction(t0), to_fraction(step)
    mg = None if max_gap is None else to_fraction(max_gap)
    by_time: Dict[Fraction, List[Tuple[int, Fraction]]] = {}
    for sid, t, v in samples:
        by_time.setdefault(to_fraction(t), []).append((sid, to_fraction(v)))
    times = sorted(by_time)
    breaks = sorted({to_fraction(b) for b in breakpoints})
    points = []
    for k in range(count):
        t = t0f + k * stepf
        points.append(_make_point(k, t, mode, _compute_point(mode, times, by_time, breaks, mg, t)))
    return points


class GridConsumer:
    """消费修订、在下游恢复完整网格的例子。

    先 bootstrap 一份初始快照，之后只应用 Revision 流，
    即可始终得到与重采样器一致的完整网格。
    """

    def __init__(self, count: int):
        self._points: List[Optional[GridPoint]] = [None] * count

    def bootstrap(self, points: List[GridPoint]):
        for p in points:
            self._points[p.index] = p

    def apply(self, revisions: List[Revision]):
        for rev in revisions:
            self._points[rev.index] = rev.new

    @property
    def points(self) -> List[Optional[GridPoint]]:
        return list(self._points)
