"""可微（手动反向）算子：dropout、高斯重参数化、checkpoint 重算。

所有算子的随机性都来自显式传入的 Key；checkpoint 保存的是键而不是
中间激活，重算时用同一键重放前向，因此 dropout mask 必然逐位复现。
"""
from __future__ import annotations

import math
from typing import Callable

from .prng import Key
from .random import bernoulli_tensor, normal_tensor
from .tensor import Tensor


def dropout_forward(x: Tensor, key: Key, p: float) -> tuple[Tensor, Tensor]:
    """反向 dropout：mask ~ Bernoulli(1-p) / (1-p)，返回 (输出, mask)。"""
    if not 0.0 <= p < 1.0:
        raise ValueError(f"dropout 概率 p 必须在 [0,1)，得到 {p}")
    keep = 1.0 - p
    mask = bernoulli_tensor(key, x.shape, keep).map(lambda b: b / keep)
    return x * mask, mask


def dropout_backward(grad_out: Tensor, mask: Tensor) -> Tensor:
    """梯度按前向保存的 mask 传播：dL/dx = dL/dy * mask。"""
    return grad_out * mask


def gaussian_reparam_sample(
    mu: Tensor, log_sigma: Tensor, key: Key
) -> tuple[Tensor, Tensor]:
    """z = μ + σ·ε，σ = exp(log_σ)，ε ~ N(0, I)。返回 (z, ε)。"""
    if mu.shape != log_sigma.shape:
        raise ValueError("mu 与 log_sigma 形状必须一致")
    eps = normal_tensor(key, mu.shape)
    sigma = log_sigma.map(math.exp)
    return mu + eps * sigma, eps


def gaussian_reparam_backward(
    grad_z: Tensor, eps: Tensor, log_sigma: Tensor
) -> tuple[Tensor, Tensor]:
    """dL/dμ = dL/dz；dL/dlog_σ = dL/dz · ε · σ。"""
    sigma = log_sigma.map(math.exp)
    return grad_z, grad_z * eps * sigma


class Checkpoint:
    """保存 (函数, 键) 而非中间结果；recompute 用同一键重放前向。

    这保证重算时 dropout 等随机算子复用原始 mask，而不是重新抽样。
    """

    def __init__(self, fn: Callable, key: Key):
        self.fn = fn
        self.key = key

    def forward(self, *args):
        return self.fn(self.key, *args)

    def recompute(self, *args):
        return self.fn(self.key, *args)
