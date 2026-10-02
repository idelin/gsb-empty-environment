"""重采样器：重采样单位与边界规则全部显式。

所有函数接受调用方提供的 random.Random 实例（由 StreamFactory 分流），
本模块自身不持有任何全局随机状态。
"""
from __future__ import annotations

import math

CIRCULAR = "circular"
TRUNCATED = "truncated"
_BOUNDARIES = (CIRCULAR, TRUNCATED)


def resample_one(sample, rng):
    """单位：单个观测；有放回抽 n 次，n 为原样本量。"""
    vals = sample.values
    n = len(vals)
    return tuple(vals[rng.randrange(n)] for _ in range(n))


def resample_two(sample, rng):
    """单位：组内单个观测；两组各自独立有放回重抽，组大小固定。"""
    nx, ny = len(sample.x), len(sample.y)
    rx = tuple(sample.x[rng.randrange(nx)] for _ in range(nx))
    ry = tuple(sample.y[rng.randrange(ny)] for _ in range(ny))
    return rx, ry


def resample_paired(sample, rng):
    """单位：观测对；抽下标后成对取出，绝不拆对、绝不逐行独立抽样。"""
    n = len(sample.x)
    idx = [rng.randrange(n) for _ in range(n)]
    rx = tuple(sample.x[i] for i in idx)
    ry = tuple(sample.y[i] for i in idx)
    return rx, ry


def resample_stratified(sample, rng):
    """单位：层内单个观测；每层内部有放回重抽，层大小固定。"""
    return tuple(
        tuple(s[rng.randrange(len(s))] for _ in range(len(s)))
        for s in sample.strata
    )


def resample_mbb(sample, block_length, rng, boundary=CIRCULAR):
    """移动块 bootstrap（moving block bootstrap）。

    单位：长度 block_length 的重叠块；抽取 k = ceil(n / block_length) 个块，
    拼接后截断至原长度 n，以保持序列内的局部相关性。

    边界规则（显式）：
    - "circular"：序列视为环，块起点取 0..n-1，越界部分回绕到序列头部；
    - "truncated"：块必须完整落在序列内，块起点取 0..n-block_length。
    """
    vals = sample.values
    n = len(vals)
    if isinstance(block_length, bool) or not isinstance(block_length, int):
        raise TypeError(f"block_length 必须为 int，得到 {type(block_length).__name__}")
    if not 1 <= block_length <= n:
        raise ValueError(f"block_length 必须在 [1, {n}] 内，得到 {block_length}")
    if boundary not in _BOUNDARIES:
        raise ValueError(f"未知边界规则: {boundary!r}，可选 {_BOUNDARIES}")
    n_blocks = math.ceil(n / block_length)
    out = []
    for _ in range(n_blocks):
        if boundary == CIRCULAR:
            start = rng.randrange(n)
            out.extend(vals[(start + j) % n] for j in range(block_length))
        else:
            start = rng.randrange(n - block_length + 1)
            out.extend(vals[start:start + block_length])
    return tuple(out[:n])
