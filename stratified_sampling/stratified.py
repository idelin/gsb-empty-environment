"""分层均值与方差估计(各层总体量 N_h 与样本量 n_h 已知)。

估计量(标准分层随机抽样):
    均值:  ybar_st = sum_h W_h * ybar_h,  W_h = N_h / N
    方差:  v(ybar_st) = sum_h W_h^2 * (1 - n_h/N_h) * s_h^2 / n_h

规则:
- 层权之和规范为 1(构造时校验);
- 大小不同的层按 N_h 加权,绝不简单平均层均值;
- 缺层(N_h > 0 但 n_h = 0)显式拒绝,抛 MissingStratumError;
- 单样本层(n_h = 1 且 N_h > 1)无法给出精确的层内方差:
  均值仍然估计,但整体方差标记为不可估(variance=None),并记录诊断;
- 全量抽样层(n_h == N_h)方差贡献为 0(有限总体修正因子为 0)。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from math import sqrt
from statistics import fmean


class SamplingDesignError(ValueError):
    """抽样设计或样本数据不合法。"""


class MissingStratumError(SamplingDesignError):
    """总体中存在的层在样本中缺失(n_h = 0 而 N_h > 0),显式拒绝。"""


@dataclass(frozen=True)
class StratumSample:
    """一个层的样本。

    name:     层名
    pop_size: 层总体量 N_h(已知)
    values:   样本观测值,n_h = len(values)
    """

    name: str
    pop_size: int
    values: tuple[float, ...]

    def __post_init__(self) -> None:
        if self.pop_size < 1:
            raise SamplingDesignError(
                f"层 {self.name!r} 的总体量 N_h 必须 >= 1,得到 {self.pop_size}"
            )
        n = len(self.values)
        if n == 0:
            raise MissingStratumError(
                f"层 {self.name!r} 在总体中存在(N_h={self.pop_size})但样本缺失,拒绝估计"
            )
        if n > self.pop_size:
            raise SamplingDesignError(
                f"层 {self.name!r} 样本量 n_h={n} 超过总体量 N_h={self.pop_size}"
            )


@dataclass(frozen=True)
class VarianceDiagnostic:
    """方差可估性诊断。"""

    stratum: str
    reason: str


@dataclass(frozen=True)
class StratifiedEstimate:
    """分层均值估计结果。

    variance 为 None 表示方差不可估(见 diagnostics),绝不给出假装精确的方差。
    """

    mean: float
    variance: float | None
    standard_error: float | None
    variance_estimable: bool
    weights: dict[str, float]
    sample_sizes: dict[str, int]
    pop_sizes: dict[str, int]
    diagnostics: tuple[VarianceDiagnostic, ...] = field(default_factory=tuple)


def _sample_variance(values: tuple[float, ...]) -> float:
    """无偏样本方差(ddof=1)。调用方保证 len(values) >= 2。"""
    n = len(values)
    mu = fmean(values)
    return sum((x - mu) ** 2 for x in values) / (n - 1)


def estimate_stratified_mean(strata: list[StratumSample]) -> StratifiedEstimate:
    """由分层样本估计总体均值及其方差。"""
    if not strata:
        raise SamplingDesignError("至少需要一个层")

    names = [s.name for s in strata]
    if len(set(names)) != len(names):
        raise SamplingDesignError("层名重复")

    pop_sizes = {s.name: s.pop_size for s in strata}
    sample_sizes = {s.name: len(s.values) for s in strata}
    total_pop = sum(pop_sizes.values())

    # 层权 W_h = N_h / N,大小不同的层按总体量加权,且权重之和规范为 1。
    weights = {name: n_h / total_pop for name, n_h in pop_sizes.items()}
    weight_sum = sum(weights.values())
    if abs(weight_sum - 1.0) > 1e-12:  # 防御性校验,构造上恒为 1
        raise SamplingDesignError(f"层权之和必须为 1,得到 {weight_sum}")

    mean = sum(weights[s.name] * fmean(s.values) for s in strata)

    variance = 0.0
    estimable = True
    diagnostics: list[VarianceDiagnostic] = []
    for s in strata:
        n_h = len(s.values)
        n_pop = s.pop_size
        w_h = weights[s.name]
        if n_h == n_pop:
            # 全量抽样层:有限总体修正因子 (1 - n_h/N_h) = 0,方差贡献为 0。
            continue
        if n_h == 1:
            # 单样本层:层内方差不可估,不假装有精确方差。
            estimable = False
            diagnostics.append(
                VarianceDiagnostic(
                    stratum=s.name,
                    reason=(
                        f"单样本层(n_h=1, N_h={n_pop}):层内方差不可估,"
                        "整体方差标记为不可估而非伪造精确值"
                    ),
                )
            )
            continue
        s2 = _sample_variance(s.values)
        fpc = 1.0 - n_h / n_pop  # 有限总体修正
        variance += w_h * w_h * fpc * s2 / n_h

    return StratifiedEstimate(
        mean=mean,
        variance=variance if estimable else None,
        standard_error=sqrt(variance) if estimable else None,
        variance_estimable=estimable,
        weights=weights,
        sample_sizes=sample_sizes,
        pop_sizes=pop_sizes,
        diagnostics=tuple(diagnostics),
    )
