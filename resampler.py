"""可更正的等间隔重采样器。

把不规则采样转换到固定时间网格，支持：
- 右连续阶梯(step) / 线性(linear) 两种模式
- 显式断点与超过上限的缺测间隔禁止跨越插值
- 每个网格点附带所用原始采样 ID、模式与有效性
- 迟到样本与撤回产生修订(Revision)
- 固定插值精度与舍入规则，网格时间保持精确位置
- 水位线封闭(seal)后释放不再需要的历史样本
"""

from __future__ import annotations

import bisect
import math
from dataclasses import dataclass
from decimal import ROUND_HALF_UP, Decimal, localcontext
from enum import Enum
from fractions import Fraction
from typing import Iterable, List, Optional, Sequence, Tuple, Union

Number = Union[int, float, str, Decimal, Fraction]

DEFAULT_PLACES = 6  # 固定插值精度：保留 6 位小数


class Mode(Enum):
    STEP = "step"      # 右连续阶梯：取最近一个 t <= 网格点的样本
    LINEAR = "linear"  # 线性：在左右Bracket样本间插值


class Reason(Enum):
    OK = "ok"
    NO_DATA = "no_data"        # 该侧没有任何有效样本（数据边缘）
    GAP = "gap"                # 相邻样本间隔超过 max_gap，缺测段禁止跨越
    BREAKPOINT = "breakpoint"  # 被显式断点阻断


@dataclass(frozen=True)
class Sample:
    """输入样本。version 用于同时间多样本裁决：大者生效；缺省按摄入顺序自增。"""

    id: str
    t: Number
    v: Number
    version: Optional[int] = None


@dataclass(frozen=True)
class GridPoint:
    """一个输出网格点的完整状态。"""

    t: Fraction                      # 精确网格位置
    value: Optional[Decimal]         # 无效时为 None
    valid: bool
    mode: Mode
    source_ids: Tuple[str, ...]      # 所用的原始采样 ID
    reason: Reason


@dataclass(frozen=True)
class Revision:
    """一次摄入/撤回产生的修订：发生变化的网格点的新状态。"""

    seq: int
    changed: Tuple[GridPoint, ...]


@dataclass(frozen=True)
class _Record:
    id: str
    t: Fraction
    v: Fraction
    version: int


def to_fraction(x: Number) -> Fraction:
    """精确地把输入转成有理数。float 按最短十进制表示解析（0.1 -> 1/10）。"""
    if isinstance(x, Fraction):
        return x
    if isinstance(x, bool):
        raise TypeError("bool is not a number here")
    if isinstance(x, int):
        return Fraction(x)
    if isinstance(x, Decimal):
        return Fraction(x)
    if isinstance(x, float):
        return Fraction(str(x))
    if isinstance(x, str):
        return Fraction(x)
    raise TypeError(f"unsupported number: {x!r}")


def round_value(fr: Fraction, places: int = DEFAULT_PLACES) -> Decimal:
    """固定舍入规则：ROUND_HALF_UP（ ties 远离零），保留 places 位小数。"""
    quantum = Decimal(1).scaleb(-places)
    with localcontext() as ctx:
        ctx.prec = max(50, places + len(str(abs(fr.numerator))) + 10)
        dec = Decimal(fr.numerator) / Decimal(fr.denominator)
        return dec.quantize(quantum, rounding=ROUND_HALF_UP)


class Resampler:
    """固定网格重采样器。

    参数:
        origin:      网格起点（可为负）
        step:        网格步长（可为非整数，精确保存）
        count:       网格点数
        mode:        "step" 或 "linear"
        max_gap:     缺测间隔上限，超过则禁止跨越插值
        breakpoints: 显式断点时间，左右样本不得跨断点组合
        places:      输出小数位（默认 6，ROUND_HALF_UP）
    """

    def __init__(
        self,
        *,
        origin: Number = 0,
        step: Number,
        count: int,
        mode: Union[str, Mode],
        max_gap: Number,
        breakpoints: Sequence[Number] = (),
        places: int = DEFAULT_PLACES,
    ):
        self.origin = to_fraction(origin)
        self.step = to_fraction(step)
        if self.step <= 0:
            raise ValueError("step must be positive")
        self.count = int(count)
        if self.count <= 0:
            raise ValueError("count must be positive")
        self.mode = mode if isinstance(mode, Mode) else Mode(mode)
        self.max_gap = to_fraction(max_gap)
        if self.max_gap < 0:
            raise ValueError("max_gap must be >= 0")
        self.breakpoints: Tuple[Fraction, ...] = tuple(
            sorted(to_fraction(b) for b in breakpoints)
        )
        self.places = places

        self._samples: dict[str, _Record] = {}
        self._auto_version = 0
        self._eff_times: List[Fraction] = []   # 有效样本时间（升序）
        self._eff_at: dict[Fraction, _Record] = {}
        self._eff_dirty = True
        self._watermark: Optional[Fraction] = None
        self._rev_seq = 0
        self._cache: List[GridPoint] = [self._compute(i) for i in range(self.count)]

    # ------------------------------------------------------------------ 网格

    def grid_time(self, i: int) -> Fraction:
        """第 i 个网格点的精确时间。"""
        return self.origin + i * self.step

    def grid(self) -> List[GridPoint]:
        """完整网格当前状态。"""
        return list(self._cache)

    def query(self, start: Number = None, end: Number = None) -> List[GridPoint]:
        """查询 [start, end) 内的网格点；缺省返回整段。"""
        lo = to_fraction(start) if start is not None else None
        hi = to_fraction(end) if end is not None else None
        return [
            gp
            for gp in self._cache
            if (lo is None or gp.t >= lo) and (hi is None or gp.t < hi)
        ]

    # ------------------------------------------------------------------ 摄入 / 撤回

    def ingest(self, samples: Iterable[Union[Sample, tuple]]) -> Revision:
        """摄入一批样本（可含迟到样本）。同 id 视为更新。返回修订。"""
        touched: List[Fraction] = []
        for item in samples:
            if isinstance(item, Sample):
                sid, t, v, ver = item.id, item.t, item.v, item.version
            else:
                sid, t, v = item[0], item[1], item[2]
                ver = item[3] if len(item) > 3 else None
            t = to_fraction(t)
            v = to_fraction(v)
            if self._watermark is not None and t < self._watermark:
                raise ValueError(
                    f"sample {sid!r} at {t} is below sealed watermark {self._watermark}"
                )
            if ver is None:
                self._auto_version += 1
                ver = self._auto_version
            old = self._samples.get(sid)
            if old is not None:
                touched.append(old.t)
            self._samples[sid] = _Record(str(sid), t, v, int(ver))
            touched.append(t)
        return self._after_mutation(touched)

    def retract(self, ids: Iterable[str]) -> Revision:
        """撤回样本。未知 id 为 no-op。返回修订。"""
        touched: List[Fraction] = []
        for sid in ids:
            old = self._samples.pop(str(sid), None)
            if old is None:
                continue
            if self._watermark is not None and old.t < self._watermark:
                self._samples[old.id] = old  # 已封闭区间不允许修改，回滚
                raise ValueError(
                    f"sample {sid!r} at {old.t} is below sealed watermark {self._watermark}"
                )
            touched.append(old.t)
        return self._after_mutation(touched)

    # ------------------------------------------------------------------ 封闭 / 释放

    def seal(self, watermark: Number) -> List[str]:
        """声明水位线：不再接受 t < watermark 的样本或撤回。

        释放规则：有效样本 s 的影响区间是 (prev(s), next(s))，
        当 next(s) <= watermark 时其影响区间整体封闭，s 及同时间被遮蔽的
        样本即可释放。返回被释放的样本 id。
        """
        w = to_fraction(watermark)
        if self._eff_dirty:
            self._rebuild()
        times = self._eff_times
        drop_times = set()
        for idx, t in enumerate(times):
            if t >= w:
                break
            nxt = times[idx + 1] if idx + 1 < len(times) else None
            if nxt is not None and nxt <= w:
                drop_times.add(t)
        released = sorted(
            sid for sid, s in self._samples.items() if s.t in drop_times
        )
        if drop_times:
            self._samples = {
                sid: s for sid, s in self._samples.items() if s.t not in drop_times
            }
            self._eff_dirty = True
        self._watermark = w if self._watermark is None else max(self._watermark, w)
        return released

    # ------------------------------------------------------------------ 内部

    def _rebuild(self) -> None:
        """重建有效样本索引：同一时间的多样本取 (version, id) 最大者。"""
        best: dict[Fraction, _Record] = {}
        for rec in self._samples.values():
            cur = best.get(rec.t)
            if cur is None or (rec.version, rec.id) > (cur.version, cur.id):
                best[rec.t] = rec
        self._eff_at = best
        self._eff_times = sorted(best)
        self._eff_dirty = False

    def _after_mutation(self, touched: List[Fraction]) -> Revision:
        self._eff_dirty = True
        self._rev_seq += 1
        if not touched:
            return Revision(self._rev_seq, ())
        self._rebuild()
        times = self._eff_times
        grid_lo = self.grid_time(0)
        grid_hi = self.grid_time(self.count - 1)
        lo_t: Optional[Fraction] = None
        hi_t: Optional[Fraction] = None
        for t in touched:
            j = bisect.bisect_left(times, t)
            prev = times[j - 1] if j > 0 else None
            k = bisect.bisect_right(times, t)
            nxt = times[k] if k < len(times) else None
            # 一个时间点的变化只影响 (prev, next) 内的网格点，含端点重算以保安全
            a = prev if prev is not None else grid_lo
            b = nxt if nxt is not None else grid_hi
            lo_t = a if lo_t is None else min(lo_t, a)
            hi_t = b if hi_t is None else max(hi_t, b)
        i_lo = max(0, math.ceil((lo_t - self.origin) / self.step))
        i_hi = min(self.count - 1, math.floor((hi_t - self.origin) / self.step))
        if self._watermark is not None:
            # 已封闭区间冻结，不再重算
            w_lo = math.ceil((self._watermark - self.origin) / self.step)
            i_lo = max(i_lo, w_lo)
        changed: List[GridPoint] = []
        for i in range(i_lo, i_hi + 1):
            gp = self._compute(i)
            if gp != self._cache[i]:
                changed.append(gp)
                self._cache[i] = gp
        return Revision(self._rev_seq, tuple(changed))

    def _compute(self, i: int) -> GridPoint:
        t = self.grid_time(i)
        if self._eff_dirty:
            self._rebuild()
        times = self._eff_times

        hit = self._eff_at.get(t)
        if hit is not None:
            return GridPoint(
                t, round_value(hit.v, self.places), True, self.mode, (hit.id,), Reason.OK
            )

        bps = self.breakpoints
        lo_idx = bisect.bisect_right(bps, t) - 1
        lo_b = bps[lo_idx] if lo_idx >= 0 else None      # 最大断点 <= t
        hi_idx = bisect.bisect_right(bps, t)
        hi_b = bps[hi_idx] if hi_idx < len(bps) else None  # 最小断点 > t

        j = bisect.bisect_left(times, t)
        left_t = times[j - 1] if j > 0 else None          # 最近有效样本 < t
        k = bisect.bisect_right(times, t)
        right_t = times[k] if k < len(times) else None    # 最近有效样本 > t

        left_blocked = right_blocked = False
        if left_t is not None and lo_b is not None and left_t < lo_b:
            left_t, left_blocked = None, True
        if right_t is not None and hi_b is not None and right_t >= hi_b:
            right_t, right_blocked = None, True

        if self.mode is Mode.STEP:
            if left_t is None:
                reason = Reason.BREAKPOINT if left_blocked else Reason.NO_DATA
                return GridPoint(t, None, False, self.mode, (), reason)
            if t - left_t > self.max_gap:
                return GridPoint(t, None, False, self.mode, (), Reason.GAP)
            rec = self._eff_at[left_t]
            return GridPoint(
                t, round_value(rec.v, self.places), True, self.mode, (rec.id,), Reason.OK
            )

        # linear
        if left_t is not None and right_t is not None:
            if right_t - left_t > self.max_gap:
                return GridPoint(t, None, False, self.mode, (), Reason.GAP)
            left = self._eff_at[left_t]
            right = self._eff_at[right_t]
            fr = left.v + (right.v - left.v) * Fraction(t - left_t, right_t - left_t)
            return GridPoint(
                t,
                round_value(fr, self.places),
                True,
                self.mode,
                (left.id, right.id),
                Reason.OK,
            )
        blocked = left_blocked or right_blocked
        return GridPoint(
            t, None, False, self.mode, (), Reason.BREAKPOINT if blocked else Reason.NO_DATA
        )
