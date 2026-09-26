"""Laplace 噪声机制（纯标准库实现）。

定义：
- 查询内容中声明 L1 敏感度 sensitivity 与隐私参数 epsilon。
- 发布值 = 真实值 + Laplace(loc=0, scale=sensitivity / epsilon)。
- 组合规则：明示 epsilon 求和（纯 DP 的顺序组合上界）。

注意：随机状态（种子、RNG 内部状态）属于秘密，绝不写入公开日志。
"""

from __future__ import annotations

import math
import random


def laplace_sample(rng: random.Random, scale: float) -> float:
    """按逆变换采样 Laplace(0, scale)。scale 必须为正。"""
    if scale <= 0:
        raise ValueError("scale must be positive")
    # 避免 log(0)：将 uniform 限制在开区间 (0, 1)。
    u = rng.random()
    while u <= 0.0:
        u = rng.random()
    u -= 0.5
    return -scale * math.copysign(1.0, u) * math.log(1.0 - 2.0 * abs(u))


def default_rng() -> random.Random:
    """默认使用操作系统熵源；测试可注入固定种子的 random.Random。"""
    return random.SystemRandom()
