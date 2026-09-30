"""Full-array reference convolution used to validate the streaming engine.

Computes each output sample directly from the complete input array with no
streaming state, so arbitrary chunking, tail flushing and overlapping delay
changes in the engine can be cross-checked against it.
"""
from __future__ import annotations

import math

from .engine import CONTAMINATED, ZERO_PAD
from .missing import intervals_contain


def render_channel(samples, schedule, missing, coefficients,
                   kernel_length, start, stop):
    """Render one channel over output positions [start, stop).

    Returns (data, flags); out-of-range input reads as zero and is flagged
    ZERO_PAD, windows overlapping missing input are flagged CONTAMINATED.
    """
    half = kernel_length // 2
    final = len(samples)
    data, flags = [], []
    for n in range(start, stop):
        delay = schedule.delay_at(n)
        p = n - delay
        m = math.floor(p)
        taps = coefficients(p - m)
        acc = 0.0
        valid = True
        contaminated = False
        for k in range(kernel_length):
            t = m - half + 1 + k
            if t < 0 or t >= final:
                valid = False
                continue
            if intervals_contain(missing, t):
                contaminated = True
                continue
            acc += taps[k] * samples[t]
        flag = 0 if valid else ZERO_PAD
        if contaminated:
            flag |= CONTAMINATED
        data.append(acc)
        flags.append(flag)
    return data, flags
