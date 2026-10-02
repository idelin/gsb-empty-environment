"""置信区间：百分位法与 BCa 分开实现，互不共用调整逻辑。"""
from __future__ import annotations

from dataclasses import dataclass
from statistics import NormalDist

from .errors import BCANotEstimableError
from .jackknife import acceleration
from .quantile import quantile

_NORM = NormalDist()

PERCENTILE = "percentile"
BCA = "bca"


@dataclass(frozen=True)
class Interval:
    low: float
    high: float
    alpha: float
    method: str           # "percentile" | "bca"
    quantile_method: str  # 分位数定义，显式记录


@dataclass(frozen=True)
class DegenerateInterval:
    """退化分布：重采样统计量为常数，区间退化为一点，单独报告。"""

    point: float
    alpha: float
    reason: str


def percentile_interval(boot_stats, alpha, quantile_method="type7"):
    """百分位区间 [Q(alpha/2), Q(1-alpha/2)]，不做任何偏差/加速度修正。"""
    _check(alpha, boot_stats)
    low = quantile(boot_stats, alpha / 2, quantile_method)
    high = quantile(boot_stats, 1 - alpha / 2, quantile_method)
    return Interval(low, high, alpha, PERCENTILE, quantile_method)


def bca_interval(boot_stats, observed, jackknife_stats, alpha, quantile_method="type7"):
    """BCa 区间。z0 或加速度不可估时显式抛 BCANotEstimableError。"""
    _check(alpha, boot_stats)
    stats = list(boot_stats)
    n_b = len(stats)
    n_less = sum(1 for b in stats if b < observed)
    prop = n_less / n_b
    if prop <= 0.0 or prop >= 1.0:
        side = "全部 >=" if prop <= 0.0 else "全部 <"
        raise BCANotEstimableError(
            f"重采样统计量{side}观测统计量，偏差修正 z0 为无穷，BCa 不可估"
        )
    z0 = _NORM.inv_cdf(prop)
    a = acceleration(jackknife_stats)  # 退化时显式抛 AccelerationNotEstimableError
    bounds = []
    for p in (alpha / 2, 1 - alpha / 2):
        z_p = _NORM.inv_cdf(p)
        denom = 1.0 - a * (z0 + z_p)
        if denom == 0.0:
            raise BCANotEstimableError("BCa 调整分母为零，修正分位数不可估")
        adj = _NORM.cdf(z0 + (z0 + z_p) / denom)
        if not 0.0 < adj < 1.0:
            raise BCANotEstimableError(
                f"BCa 修正分位数 {adj!r} 超出 (0, 1)，不可估"
            )
        bounds.append(quantile(stats, adj, quantile_method))
    return Interval(bounds[0], bounds[1], alpha, BCA, quantile_method)


def _check(alpha, boot_stats):
    if not 0.0 < alpha < 1.0:
        raise ValueError(f"alpha 必须在 (0, 1) 内，得到 {alpha!r}")
    if len(boot_stats) < 2:
        raise ValueError(f"至少需要 2 个重采样统计量，得到 {len(boot_stats)}")
