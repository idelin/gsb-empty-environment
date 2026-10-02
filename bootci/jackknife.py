"""Jackknife（留一法）：为 BCa 提供加速度估计。

退化（留一值全部相同或数量不足）时显式抛 AccelerationNotEstimableError，
绝不返回占位值。
"""
from __future__ import annotations

from .errors import AccelerationNotEstimableError
from .samples import OneSample, PairedSample, StratifiedSample, TwoSample


def jackknife(sample, statistic):
    """按样本结构做留一法，返回留一统计量列表。

    - OneSample：每次删一个观测；
    - PairedSample：每次删一对（保持配对结构）；
    - TwoSample：轮流删两组中的每个观测；
    - StratifiedSample：每次删某层内一个观测，其余层不变；
    - TimeSeriesSample：不支持（需块 jackknife），显式抛 TypeError。
    """
    if isinstance(sample, OneSample):
        vals = sample.values
        _require(len(vals), "单样本")
        return [statistic(vals[:i] + vals[i + 1:]) for i in range(len(vals))]
    if isinstance(sample, PairedSample):
        n = len(sample.x)
        _require(n, "配对样本")
        return [
            statistic(sample.x[:i] + sample.x[i + 1:], sample.y[:i] + sample.y[i + 1:])
            for i in range(n)
        ]
    if isinstance(sample, TwoSample):
        _require(len(sample.x) + len(sample.y), "成组两样本")
        stats = [
            statistic(sample.x[:i] + sample.x[i + 1:], sample.y)
            for i in range(len(sample.x))
        ]
        stats += [
            statistic(sample.x, sample.y[:j] + sample.y[j + 1:])
            for j in range(len(sample.y))
        ]
        return stats
    if isinstance(sample, StratifiedSample):
        stats = []
        for k, stratum in enumerate(sample.strata):
            _require(len(stratum), f"第 {k} 层")
            for i in range(len(stratum)):
                new_strata = list(sample.strata)
                new_strata[k] = stratum[:i] + stratum[i + 1:]
                stats.append(statistic(tuple(new_strata)))
        return stats
    raise TypeError(
        f"样本类型 {type(sample).__name__} 不支持标准留一 jackknife"
    )


def _require(n, what):
    if n < 2:
        raise AccelerationNotEstimableError(
            f"{what}留一后样本不足（n={n}），jackknife 不可估"
        )


def acceleration(jackknife_stats):
    """BCa 加速度 a = Σ(θ̄-θᵢ)³ / (6·[Σ(θ̄-θᵢ)²]^(3/2))。

    分母为零（留一值全相同，即 jackknife 退化）时显式失败。
    """
    stats = [float(s) for s in jackknife_stats]
    n = len(stats)
    if n < 2:
        raise AccelerationNotEstimableError(
            f"jackknife 至少需要 2 个留一值，得到 {n}"
        )
    mean = sum(stats) / n
    diffs = [mean - s for s in stats]
    ss = sum(d * d for d in diffs)
    if ss == 0.0:
        raise AccelerationNotEstimableError(
            "jackknife 留一值全部相同（退化），加速度 a 不可估"
        )
    num = sum(d ** 3 for d in diffs)
    return num / (6.0 * ss ** 1.5)
