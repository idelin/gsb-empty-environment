"""Pure-python complex FFT.

Radix-2 recursive Cooley-Tukey; non-power-of-two lengths fall back to a
direct DFT so any window size works (and small inputs cross-check the
reference implementation).
"""
from __future__ import annotations

import cmath
import math


def dft_naive(x):
    n = len(x)
    out = []
    for k in range(n):
        acc = 0j
        for m in range(n):
            acc += x[m] * cmath.exp(-2j * math.pi * k * m / n)
        out.append(acc)
    return out


def _fft_radix2(x):
    n = len(x)
    if n == 1:
        return [x[0]]
    even = _fft_radix2(x[0::2])
    odd = _fft_radix2(x[1::2])
    out = [0j] * n
    half = n // 2
    for k in range(half):
        t = cmath.exp(-2j * math.pi * k / n) * odd[k]
        out[k] = even[k] + t
        out[k + half] = even[k] - t
    return out


def fft(x):
    vals = [complex(v) for v in x]
    n = len(vals)
    if n == 0:
        return []
    if n & (n - 1):
        return dft_naive(vals)
    return _fft_radix2(vals)


def ifft(x):
    n = len(x)
    if n == 0:
        return []
    conj = [complex(v).conjugate() for v in x]
    return [z.conjugate() / n for z in fft(conj)]
