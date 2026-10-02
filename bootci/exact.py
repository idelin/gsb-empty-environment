"""有限小样本精确枚举：枚举所有可能重采样并赋多项式权重。

结论为精确（exact），无抽样误差，与经验（empirical）估计严格区分。
权重为整数（多项式系数），总权重 = 各组 n_i ** n_i 之积。
"""
from __future__ import annotations

import math

from .errors import EnumerationTooLargeError
from .quantile import weighted_quantile_type1
from .samples import OneSample, PairedSample, StratifiedSample, TwoSample

DEFAULT_MAX_SUPPORT = 2_000_000


def _compositions(total, parts):
    """把 total 拆成 parts 个非负整数之和（星棒法枚举全部重采样多重集）。"""
    if parts == 1:
        yield (total,)
        return
    for first in range(total + 1):
        for rest in _compositions(total - first, parts - 1):
            yield (first,) + rest


def _n_compositions(total, parts):
    return math.comb(total + parts - 1, parts - 1)


def _multinomial(counts):
    result = math.factorial(sum(counts))
    for c in counts:
        result //= math.factorial(c)
    return result


def _expand(values, counts):
    out = []
    for v, c in zip(values, counts):
        out.extend([v] * c)
    return tuple(out)


def _check_support(combos, max_support):
    if combos > max_support:
        raise EnumerationTooLargeError(
            f"精确枚举需要 {combos} 个重采样组合，超过上限 {max_support}；"
            "请改用经验 bootstrap 或显式提高 max_support"
        )


def exact_distribution(sample, statistic, max_support=DEFAULT_MAX_SUPPORT):
    """返回 (dist, total_weight)：dist 为 {统计量值: 整数权重}。

    枚举单位为各样本类型定义的重采样单位（观测 / 对 / 层内观测）。
    """
    if isinstance(sample, OneSample):
        n = len(sample.values)
        _check_support(_n_compositions(n, n), max_support)
        dist = {}
        for counts in _compositions(n, n):
            value = statistic(_expand(sample.values, counts))
            w = _multinomial(counts)
            dist[value] = dist.get(value, 0) + w
        return dist, n ** n
    if isinstance(sample, PairedSample):
        n = len(sample.x)
        _check_support(_n_compositions(n, n), max_support)
        dist = {}
        for counts in _compositions(n, n):
            rx = _expand(sample.x, counts)
            ry = _expand(sample.y, counts)
            value = statistic(rx, ry)
            w = _multinomial(counts)
            dist[value] = dist.get(value, 0) + w
        return dist, n ** n
    if isinstance(sample, TwoSample):
        nx, ny = len(sample.x), len(sample.y)
        _check_support(
            _n_compositions(nx, nx) * _n_compositions(ny, ny), max_support
        )
        dist = {}
        for cx in _compositions(nx, nx):
            rx = _expand(sample.x, cx)
            wx = _multinomial(cx)
            for cy in _compositions(ny, ny):
                value = statistic(rx, _expand(sample.y, cy))
                w = wx * _multinomial(cy)
                dist[value] = dist.get(value, 0) + w
        return dist, nx ** nx * ny ** ny
    if isinstance(sample, StratifiedSample):
        combos = 1
        for s in sample.strata:
            combos *= _n_compositions(len(s), len(s))
        _check_support(combos, max_support)
        dist = {}

        def rec(k, acc, weight):
            if k == len(sample.strata):
                value = statistic(tuple(acc))
                dist[value] = dist.get(value, 0) + weight
                return
            s = sample.strata[k]
            for counts in _compositions(len(s), len(s)):
                rec(k + 1, acc + [_expand(s, counts)], weight * _multinomial(counts))

        rec(0, [], 1)
        total = 1
        for s in sample.strata:
            total *= len(s) ** len(s)
        return dist, total
    raise TypeError(f"样本类型 {type(sample).__name__} 不支持精确枚举")


def exact_percentile_interval(dist, total_weight, alpha):
    """基于精确分布的加权 type1 分位数区间（结论为精确，非经验估计）。"""
    if not 0.0 < alpha < 1.0:
        raise ValueError(f"alpha 必须在 (0, 1) 内，得到 {alpha!r}")
    items = sorted(dist.items())
    low = weighted_quantile_type1(items, alpha / 2, total_weight)
    high = weighted_quantile_type1(items, 1 - alpha / 2, total_weight)
    return low, high
