"""分布采样：所有随机性来自显式 Key，公式公开。

分布公式
--------
- Uniform[0,1):   u = bits(key, i)[高53位] / 2^53
- Normal(0,1):    Box-Muller。第 p 对元素取
                  u1 = 1 - uniform01(key, 2p)   （保证 u1 ∈ (0,1]）
                  u2 = uniform01(key, 2p+1)
                  z_{2p}   = sqrt(-2 ln u1) * cos(2π u2)
                  z_{2p+1} = sqrt(-2 ln u1) * sin(2π u2)
- Bernoulli(p):   b = 1[u < p]，u ~ Uniform[0,1)
- 高斯重参数化:   z = μ + σ·ε, ε ~ Normal(0,1)（见 nn.gaussian_reparam_sample）
"""
from __future__ import annotations

import math

from .prng import Key, uniform01
from .tensor import Tensor, _numel


def uniform_tensor(key: Key, shape: tuple[int, ...]) -> Tensor:
    n = _numel(tuple(shape))
    return Tensor([uniform01(key, i) for i in range(n)], tuple(shape))


def normal_tensor(key: Key, shape: tuple[int, ...]) -> Tensor:
    """标准正态，Box-Muller 成对生成，奇数末尾丢弃一个正弦分量。"""
    n = _numel(tuple(shape))
    out = [0.0] * n
    for p in range((n + 1) // 2):
        u1 = 1.0 - uniform01(key, 2 * p)
        u2 = uniform01(key, 2 * p + 1)
        r = math.sqrt(-2.0 * math.log(u1))
        theta = 2.0 * math.pi * u2
        out[2 * p] = r * math.cos(theta)
        if 2 * p + 1 < n:
            out[2 * p + 1] = r * math.sin(theta)
    return Tensor(out, tuple(shape))


def bernoulli_tensor(key: Key, shape: tuple[int, ...], p: float) -> Tensor:
    if not 0.0 <= p <= 1.0:
        raise ValueError(f"Bernoulli 概率 p 必须在 [0,1]，得到 {p}")
    n = _numel(tuple(shape))
    return Tensor(
        [1.0 if uniform01(key, i) < p else 0.0 for i in range(n)], tuple(shape)
    )
