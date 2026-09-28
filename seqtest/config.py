"""二项分布两简单假设的 SPRT 配置。

H0: X ~ Bernoulli(p0),  H1: X ~ Bernoulli(p1)。
似然比上下界由调用方显式给定 (A > 1, 0 <= B < 1)，
本模块只把它们映射到对数尺度，不做任何 epsilon 平移。
"""
from __future__ import annotations

import math
from dataclasses import dataclass


def log_ratio(num: float, den: float) -> float:
    """计算 log(num / den)，对概率 0 / 1 的似然单独处理。

    - 0 < num, den   -> log(num) - log(den)
    - num > 0, den=0 -> +inf（观测在 den 下似然为 0）
    - num = 0, den>0 -> -inf
    - num = 0, den=0 -> 0（0/0 不定式，按“无证据”处理）
    """
    if num <= 0.0:
        if den <= 0.0:
            return 0.0
        return -math.inf
    if den <= 0.0:
        return math.inf
    return math.log(num) - math.log(den)


@dataclass(frozen=True)
class SprtConfig:
    """固定两假设 SPRT 的全部参数。

    bounds 接受两种写法：
      * SprtConfig(p0, p1, bounds=(A, B))，A、B 为似然比尺度 (LR >= A 接受 H1，LR <= B 接受 H0)；
      * SprtConfig(p0, p1, log_bounds=(a, b))，a>0、b<0 为对数尺度。
    n_max 为最大样本数；到达后仍在 (b, a) 开区间内则判 INCONCLUSIVE。
    """

    p0: float
    p1: float
    log_a: float
    log_b: float
    n_max: int

    def __init__(
        self,
        p0: float,
        p1: float,
        bounds: tuple[float, float] | None = None,
        *,
        log_bounds: tuple[float, float] | None = None,
        n_max: int = 100,
    ) -> None:
        if not (0.0 <= p0 <= 1.0 and 0.0 <= p1 <= 1.0):
            raise ValueError("p0, p1 必须在 [0, 1] 内")
        if p0 == p1:
            raise ValueError("两假设必须不同 (p0 != p1)")
        if not isinstance(n_max, int) or n_max <= 0:
            raise ValueError("n_max 必须是正整数")
        if bounds is not None and log_bounds is not None:
            raise ValueError("bounds 与 log_bounds 只能给一个")
        if bounds is None and log_bounds is None:
            raise ValueError("必须给定似然比上下界 bounds=(A, B) 或 log_bounds=(a, b)")
        if log_bounds is not None:
            log_a, log_b = (float(log_bounds[0]), float(log_bounds[1]))
        else:
            a_, b_ = (float(bounds[0]), float(bounds[1]))
            if not (a_ > 1.0):
                raise ValueError("似然比上界 A 必须 > 1")
            if not (0.0 <= b_ < 1.0):
                raise ValueError("似然比下界 B 必须满足 0 <= B < 1")
            log_a, log_b = math.log(a_), log_ratio(b_, 1.0)
        if not (math.isfinite(log_a) and math.isfinite(log_b)):
            raise ValueError("对数上下界必须有限")
        if not (log_b < 0.0 < log_a):
            raise ValueError("必须满足 b < 0 < a")
        object.__setattr__(self, "p0", float(p0))
        object.__setattr__(self, "p1", float(p1))
        object.__setattr__(self, "log_a", log_a)
        object.__setattr__(self, "log_b", log_b)
        object.__setattr__(self, "n_max", n_max)

    @property
    def bounds(self) -> tuple[float, float]:
        """似然比尺度的 (A, B)。"""
        return (math.exp(self.log_a), math.exp(self.log_b))

    def weight(self, x: int) -> float:
        """单个伯努利观测 x 的对数似然比增量 log f1(x)/f0(x)。"""
        if x == 1:
            return log_ratio(self.p1, self.p0)
        if x == 0:
            return log_ratio(1.0 - self.p1, 1.0 - self.p0)
        raise ValueError("观测必须是 0 或 1")

    def canonical_log_lr(self, n: int, k: int) -> float:
        """由充分统计量 (n 次试验中 k 次成功) 计算对数累计似然比。

        采用 k*w1 + (n-k)*w0 的整数计数累计式（w1,w0 为单样本对数权重），
        在线过程与精确递推共用本函数，保证两边逐位一致、无浮点漂移；
        k=0 的项按 0 处理，从而 p=0/1 时的 inf 权重不会产生 0*inf=nan。
        """
        if n == 0:
            return 0.0
        w1 = self.weight(1)
        w0 = self.weight(0)
        term1 = k * w1 if k > 0 else 0.0
        term0 = (n - k) * w0 if (n - k) > 0 else 0.0
        return term1 + term0

    def to_dict(self) -> dict:
        return {
            "p0": self.p0,
            "p1": self.p1,
            "log_a": self.log_a,
            "log_b": self.log_b,
            "n_max": self.n_max,
        }

    @classmethod
    def from_dict(cls, d: dict) -> "SprtConfig":
        obj = object.__new__(cls)
        object.__setattr__(obj, "p0", float(d["p0"]))
        object.__setattr__(obj, "p1", float(d["p1"]))
        object.__setattr__(obj, "log_a", float(d["log_a"]))
        object.__setattr__(obj, "log_b", float(d["log_b"]))
        object.__setattr__(obj, "n_max", int(d["n_max"]))
        return obj
