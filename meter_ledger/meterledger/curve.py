"""采样曲线:有理数采样点、阶梯/线性插值、断测区间与校准时间线。

所有时间与值都是 fractions.Fraction。曲线状态只增不改:
采样点、断测区间、校准规则都带版本号,积分可以按任意历史版本
组合重放,这是增量修正与崩溃恢复的基础。
"""
from bisect import bisect_right
from fractions import Fraction


def fr(x):
    """把 int / str / Fraction 统一成 Fraction。"""
    if isinstance(x, Fraction):
        return x
    return Fraction(x)


class Sample:
    __slots__ = ("seq", "t", "v")

    def __init__(self, seq, t, v):
        self.seq = seq
        self.t = t
        self.v = v


class Gap:
    __slots__ = ("seq", "a", "b")

    def __init__(self, seq, a, b):
        self.seq = seq
        self.a = a
        self.b = b


class Calibration:
    __slots__ = ("version", "effective_from", "scale", "offset")

    def __init__(self, version, effective_from, scale, offset):
        self.version = version
        self.effective_from = effective_from
        self.scale = scale
        self.offset = offset


class Curve:
    """只增不改的采样曲线,支持按版本重放任意历史状态。"""

    def __init__(self, mode="step"):
        if mode not in ("step", "linear"):
            raise ValueError(f"未知插值模式: {mode}")
        self.mode = mode
        self.samples = []        # 按时刻排序的 Sample
        self.gaps = []           # 用户声明的断测区间
        self.calibrations = []   # 校准规则时间线

    def add_sample(self, seq, t, v):
        t, v = fr(t), fr(v)
        ts = [s.t for s in self.samples]
        # 同一时刻的改值也追加保留(曲线只增不改),取值时按版本取最新
        self.samples.insert(bisect_right(ts, t), Sample(seq, t, v))

    def add_gap(self, seq, a, b):
        a, b = fr(a), fr(b)
        if a >= b:
            raise ValueError("断测区间必须满足 start < end")
        self.gaps.append(Gap(seq, a, b))

    def add_calibration(self, version, effective_from, scale, offset):
        self.calibrations.append(
            Calibration(version, fr(effective_from), fr(scale), fr(offset)))

    def unclosed_segment(self):
        """最后一个采样点张开的未闭合段(尾部外推段)。"""
        if not self.samples:
            return None
        last = self.samples[-1]
        return {"start": last.t, "value": last.v, "mode": self.mode}

    def _raw_value(self, t, samples):
        ts = [s.t for s in samples]
        i = bisect_right(ts, t) - 1
        if i < 0:
            return None  # 首个采样点之前:未知
        if self.mode == "step" or i == len(samples) - 1:
            return samples[i].v
        s0, s1 = samples[i], samples[i + 1]
        return s0.v + (s1.v - s0.v) * (t - s0.t) / (s1.t - s0.t)

    @staticmethod
    def _calibration_at(t, cals):
        best = None
        for c in cals:
            if c.effective_from <= t and (best is None or c.effective_from > best.effective_from):
                best = c
        if best is None:
            return Fraction(1), Fraction(0)
        return best.scale, best.offset

    def integrate(self, a, b, sample_version=None, calibration_version=None,
                  gap_version=None):
        """返回 [a, b) 的 (已知部分积分, 缺测长度)。

        断测区间与首个采样点之前都计为缺测,不参与积分(不当零值)。
        版本参数为 None 时取最新状态。
        """
        a, b = fr(a), fr(b)
        if a >= b:
            return Fraction(0), Fraction(0)
        samples = []
        for s in self.samples:
            if sample_version is not None and s.seq > sample_version:
                continue
            if samples and samples[-1].t == s.t:
                samples[-1] = s  # 同一时刻保留版本最新的改值
            else:
                samples.append(s)
        cals = [c for c in self.calibrations
                if calibration_version is None or c.version <= calibration_version]
        gaps = []
        for g in self.gaps:
            if gap_version is not None and g.seq > gap_version:
                continue
            lo, hi = max(g.a, a), min(g.b, b)
            if lo < hi:
                gaps.append((lo, hi))
        points = {a, b}
        for s in samples:
            if a < s.t < b:
                points.add(s.t)
        for lo, hi in gaps:
            points.add(lo)
            points.add(hi)
        for c in cals:
            if a < c.effective_from < b:
                points.add(c.effective_from)
        ordered = sorted(points)
        first_t = samples[0].t if samples else None
        value = Fraction(0)
        missing = Fraction(0)
        for s, e in zip(ordered, ordered[1:]):
            if first_t is None or e <= first_t:
                missing += e - s
                continue
            mid = (s + e) / 2
            if any(lo <= mid < hi for lo, hi in gaps):
                missing += e - s
                continue
            scale, offset = self._calibration_at(s, cals)
            if self.mode == "step":
                seg = self._raw_value(s, samples) * (e - s)
            else:
                seg = (self._raw_value(s, samples)
                       + self._raw_value(e, samples)) * (e - s) / 2
            value += scale * seg + offset * (e - s)
        return value, missing
