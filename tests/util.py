"""测试辅助:Goertzel 单频测量、误差与波形特征检查。"""
from __future__ import annotations

import math


def goertzel(x, start, n, freq, fs):
    """返回 x[start:start+n] 在 freq 处的 (相位rad, 幅度)。"""
    w = 2.0 * math.pi * freq / fs
    re = 0.0
    im = 0.0
    for i in range(n):
        re += x[start + i] * math.cos(w * i)
        im -= x[start + i] * math.sin(w * i)
    return math.atan2(im, re), 2.0 * math.hypot(re, im) / n


def max_abs_err(a, b):
    return max((abs(x - y) for x, y in zip(a, b)), default=0.0)


def argmax_abs(x):
    return max(range(len(x)), key=lambda t: abs(x[t]))


def has_flat_run(xs, run=3):
    """检测连续等值(硬裁平的波形特征)。"""
    count = 1
    for i in range(1, len(xs)):
        if xs[i] == xs[i - 1]:
            count += 1
            if count >= run:
                return True
        else:
            count = 1
    return False
