"""分位数函数：定义显式，不依赖任何“默认行为”。

支持两种定义：
- "type7"：Hyndman & Fan type 7 线性插值（与 numpy/R 默认一致），
  h = (n-1)*p，Q(p) = x[floor(h)] + (h - floor(h)) * (x[floor(h)+1] - x[floor(h)])。
- "type1"：经验 CDF 逆，即满足 F(x) >= p 的最小 x（阶梯函数，无插值）。
"""
from __future__ import annotations

import math

TYPE7 = "type7"
TYPE1 = "type1"
_METHODS = (TYPE7, TYPE1)


def quantile(values, p, method=TYPE7):
    """按显式定义计算分位数。values 无需有序，内部排序。"""
    if method not in _METHODS:
        raise ValueError(f"未知分位数定义: {method!r}，可选 {_METHODS}")
    if not 0.0 <= p <= 1.0:
        raise ValueError(f"p 必须在 [0, 1] 内，得到 {p!r}")
    xs = sorted(values)
    n = len(xs)
    if n == 0:
        raise ValueError("空序列无法计算分位数")
    if n == 1:
        return xs[0]
    if method == TYPE7:
        h = (n - 1) * p
        lo = math.floor(h)
        hi = min(lo + 1, n - 1)
        frac = h - lo
        return xs[lo] + frac * (xs[hi] - xs[lo])
    # type1：经验 CDF 逆
    idx = math.ceil(n * p) - 1
    return xs[min(n - 1, max(0, idx))]


def weighted_quantile_type1(sorted_items, p, total_weight):
    """加权经验 CDF 逆（type1 定义）。

    sorted_items：按值升序的 (value, weight) 序列，weight 为非负整数；
    total_weight：权重总和。用于精确枚举分布，比较基于整数权重，无抽样误差。
    """
    if not 0.0 <= p <= 1.0:
        raise ValueError(f"p 必须在 [0, 1] 内，得到 {p!r}")
    if total_weight <= 0:
        raise ValueError("总权重必须为正")
    threshold = p * total_weight
    cum = 0
    for value, weight in sorted_items:
        cum += weight
        if cum >= threshold:
            return value
    return sorted_items[-1][0]
