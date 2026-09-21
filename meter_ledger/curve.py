"""采样曲线:有理数采样、阶梯/线性插值、断测区间、分段校准。

所有时间与数值一律使用 fractions.Fraction,保证积分结果精确。
断测区间内的积分不计入已知部分,只计入缺测长度,绝不当作零值。
"""
from __future__ import annotations

from bisect import bisect_right, insort
from dataclasses import dataclass
from fractions import Fraction

STEP = "step"
LINEAR = "linear"


@dataclass(frozen=True)
class Calibration:
    """一条校准规则:自 effective_from 起,value' = scale * value + offset。"""

    version: int
    effective_from: Fraction
    scale: Fraction
    offset: Fraction

    def apply(self, value: Fraction) -> Fraction:
        return self.scale * value + self.offset


IDENTITY = Calibration(0, Fraction(0), Fraction(1), Fraction(0))


class SampleCurve:
    """支持版本号的采样曲线。sample_version 随采样/断测变更递增,
    calibration_version 随校准规则递增。"""

    def __init__(self, mode: str = STEP):
        if mode not in (STEP, LINEAR):
            raise ValueError(f"未知插值模式: {mode}")
        self.mode = mode
        self.times: list[Fraction] = []
        self.values: dict[Fraction, Fraction] = {}
        self.gaps: list[tuple[Fraction, Fraction]] = []
        self.calibrations: list[Calibration] = []
        self.sample_version = 0
        self.calibration_version = 0

    # ------------------------------------------------------------------ 变更
    def add_sample(self, time, value) -> None:
        time, value = Fraction(time), Fraction(value)
        if time not in self.values:
            insort(self.times, time)
        self.values[time] = value
        self.sample_version += 1

    def declare_gap(self, start, end) -> None:
        start, end = Fraction(start), Fraction(end)
        if not start < end:
            raise ValueError("断测区间为空")
        self.gaps.append((start, end))
        self.gaps.sort()
        self.sample_version += 1

    def add_calibration(self, effective_from, scale, offset) -> Calibration:
        self.calibration_version += 1
        cal = Calibration(
            self.calibration_version,
            Fraction(effective_from),
            Fraction(scale),
            Fraction(offset),
        )
        self.calibrations.append(cal)
        self.calibrations.sort(key=lambda c: c.effective_from)
        return cal

    # ------------------------------------------------------------------ 查询
    def calibration_at(self, t: Fraction) -> Calibration:
        """t 时刻生效的校准:生效时刻不超过 t 的最后一条规则。"""
        active = IDENTITY
        for cal in self.calibrations:
            if cal.effective_from <= t:
                active = cal
            else:
                break
        return active

    def raw_at(self, t: Fraction):
        """未校准取值;t 不在已知域内返回 None(未知,不是零)。"""
        if not self.times:
            return None
        idx = bisect_right(self.times, t) - 1
        if idx < 0:
            return None
        t0 = self.times[idx]
        if self.mode == STEP:
            return self.values[t0]
        if t > self.times[-1]:
            return None
        if t == t0:
            return self.values[t0]
        t1 = self.times[idx + 1]
        v0, v1 = self.values[t0], self.values[t1]
        return v0 + (v1 - v0) * (t - t0) / (t1 - t0)

    def in_gap(self, t: Fraction) -> bool:
        return any(s <= t < e for s, e in self.gaps)

    def integrate(self, a, b):
        """[a, b) 上的积分。返回 (已知部分积分, 缺测长度)。"""
        a, b = Fraction(a), Fraction(b)
        points = {a, b}
        points.update(t for t in self.times if a < t < b)
        for s, e in self.gaps:
            if a < s < b:
                points.add(s)
            if a < e < b:
                points.add(e)
        for cal in self.calibrations:
            if a < cal.effective_from < b:
                points.add(cal.effective_from)
        pts = sorted(points)
        known = Fraction(0)
        missing = Fraction(0)
        for t0, t1 in zip(pts, pts[1:]):
            mid = (t0 + t1) / 2
            if self.in_gap(mid):
                missing += t1 - t0
                continue
            cal = self.calibration_at(mid)
            if self.mode == STEP:
                v = self.raw_at(t0)
                if v is None:
                    missing += t1 - t0
                    continue
                known += cal.apply(v) * (t1 - t0)
            else:
                v0, v1 = self.raw_at(t0), self.raw_at(t1)
                if v0 is None or v1 is None:
                    missing += t1 - t0
                    continue
                raw = (v0 + v1) / 2 * (t1 - t0)
                known += cal.scale * raw + cal.offset * (t1 - t0)
        return known, missing

    # -------------------------------------------------------------- 影响范围
    def sample_impact(self, time):
        """若在 time 插入采样,取值会改变的时间范围 [start, end)。
        end 为 None 表示延伸到 +∞。用于只重算相交查询。"""
        time = Fraction(time)
        idx = bisect_right(self.times, time)
        prev_t = self.times[idx - 1] if idx > 0 else None
        next_t = self.times[idx] if idx < len(self.times) else None
        if self.mode == STEP:
            return time, next_t
        return (prev_t if prev_t is not None else time), next_t
