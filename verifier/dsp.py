"""Pure-Python radix-2 FFT and correlation helpers (standard library only)."""
from __future__ import annotations

import cmath
import math


def next_pow2(n: int) -> int:
    if n < 1:
        return 1
    p = 1
    while p < n:
        p <<= 1
    return p


def fft(values):
    """Iterative radix-2 DFT. Length must be a power of two."""
    n = len(values)
    if n == 0:
        return []
    if n & (n - 1):
        raise ValueError(f"fft length must be a power of two, got {n}")
    a = [complex(v) for v in values]
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
        half = size >> 1
        twiddles = [cmath.exp(-2j * math.pi * k / size) for k in range(half)]
        for base in range(0, n, size):
            for k in range(half):
                even = a[base + k]
                odd = a[base + k + half] * twiddles[k]
                a[base + k] = even + odd
                a[base + k + half] = even - odd
        size <<= 1
    return a


def ifft(values):
    n = len(values)
    if n == 0:
        return []
    out = fft([complex(v).conjugate() for v in values])
    return [v.conjugate() / n for v in out]


def dft_naive(values):
    """Reference O(n^2) DFT, used only by the test-suite."""
    n = len(values)
    out = []
    for k in range(n):
        acc = 0j
        for t, v in enumerate(values):
            acc += v * cmath.exp(-2j * math.pi * k * t / n)
        out.append(acc)
    return out


def cross_correlate(x, y):
    """r[lag] = sum_n x[n+lag] * y[n] for lag in [-(len(y)-1), len(x)-1].

    With y[n] = x[n-d] (x delayed by d), cross_correlate(y, x) peaks at
    lag = +d, which is the convention estimate_delay relies on.
    """
    n = next_pow2(len(x) + len(y))
    fx = fft([complex(v) for v in x] + [0j] * (n - len(x)))
    fy = fft([complex(v) for v in y] + [0j] * (n - len(y)))
    prod = [a * b.conjugate() for a, b in zip(fx, fy)]
    r = ifft(prod)
    lags = list(range(0, len(x))) + list(range(-(len(y) - 1), 0))
    vals = [r[lag % n].real for lag in lags]
    return lags, vals
