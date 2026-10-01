"""核心 STFT 原语：窗函数、直接 DFT 参考、FFT、分帧起点生成。

仅使用 Python 3.11 标准库。延迟与数值容差声明见 README.md。
"""

from __future__ import annotations

import cmath
import math

# 往返重建（分析窗 = 综合窗，按每样本实际重叠窗权重归一）的声明容差。
# 纯 Python float64 FFT 实测误差约 1e-12 量级，声明值取 1e-9。
ROUNDTRIP_TOL = 1e-9


def hann_window(n):
    """周期性 Hann 窗（hop = n/2, n/4, ... 时满足 COLA）。"""
    if n <= 0:
        raise ValueError("window length must be positive")
    if n == 1:
        return [1.0]
    return [0.5 - 0.5 * math.cos(2.0 * math.pi * i / n) for i in range(n)]


def dft(x):
    """直接 DFT，作为小输入的参考实现。"""
    n = len(x)
    out = []
    for k in range(n):
        acc = 0j
        for t in range(n):
            acc += x[t] * cmath.exp(-2j * math.pi * k * t / n)
        out.append(acc)
    return out


def idft(x):
    """直接逆 DFT，dft 的参考逆变换。"""
    n = len(x)
    out = []
    for t in range(n):
        acc = 0j
        for k in range(n):
            acc += x[k] * cmath.exp(2j * math.pi * k * t / n)
        out.append(acc / n)
    return out


def _is_pow2(n):
    return n > 0 and (n & (n - 1)) == 0


def fft(x):
    """迭代基-2 FFT；非 2 的幂长度回退到直接 DFT。"""
    n = len(x)
    if not _is_pow2(n):
        return dft(x)
    a = [complex(v) for v in x]
    j = 0
    for i in range(1, n):
        bit = n >> 1
        while j & bit:
            j ^= bit
            bit >>= 1
        j |= bit
        if i < j:
            a[i], a[j] = a[j], a[i]
    size = 2
    while size <= n:
        half = size // 2
        wlen = cmath.exp(-2j * math.pi / size)
        for i in range(0, n, size):
            w = 1 + 0j
            for k in range(i, i + half):
                u = a[k]
                v = a[k + half] * w
                a[k] = u + v
                a[k + half] = u - v
                w *= wlen
        size *= 2
    return a


def ifft(x):
    """基于共轭对称技巧的逆 FFT。"""
    n = len(x)
    conj = [complex(v).conjugate() for v in x]
    return [v.conjugate() / n for v in fft(conj)]


def centered_starts(num_samples, win, hop):
    """居中分帧的固定 hop 起点序列（首帧起点为 -win//2，允许短尾帧越界）。"""
    if hop <= 0:
        raise ValueError("hop must be positive")
    starts = []
    s = -(win // 2)
    while s < num_samples:
        starts.append(s)
        s += hop
    return starts


def variable_hop_starts(num_samples, win, hops):
    """变 hop 起点序列：hops 为循环使用的 hop 列表。"""
    if not hops or any(h <= 0 for h in hops):
        raise ValueError("hops must be a non-empty list of positive ints")
    starts = []
    s = -(win // 2)
    i = 0
    while s < num_samples:
        starts.append(s)
        s += hops[i % len(hops)]
        i += 1
    return starts
