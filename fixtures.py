"""Local test fixtures: deterministic sample arrays and kernels.

No audio device is involved; everything is generated in memory.
The declared fixture sample rate is 48000 Hz.
"""

from __future__ import annotations

import math

SAMPLE_RATE = 48000.0

# Kernel library.  identity / lp2 / lp4 / long32 all have DC gain 1.0
# (equal-gain set, used for the no-step constant-signal checks).
# Coefficients are powers of two where possible so float64 results are exact.
KERNELS = {
    "identity": [1.0],
    "lp2": [0.5, 0.5],
    "lp4": [0.25, 0.25, 0.25, 0.25],
    "hp2": [0.5, -0.5],
    "delay3": [0.0, 0.0, 0.0, 1.0],
    "echo": [0.75, 0.0, 0.0, 0.25],
    "long32": [1.0 / 32.0] * 32,
    "long64": [1.0 / 64.0] * 64,
}


def impulse(length, index=0, amplitude=1.0):
    """Unit impulse of `amplitude` at `index`, zeros elsewhere."""
    data = [0.0] * length
    data[index] = amplitude
    return data


def constant(value, length):
    return [float(value)] * length


def sine(freq, length, sample_rate=SAMPLE_RATE, amplitude=1.0, phase=0.0):
    return [
        amplitude * math.sin(phase + 2.0 * math.pi * freq * n / sample_rate)
        for n in range(length)
    ]


def steps(levels, segment_length):
    """Piecewise-constant signal: each level held for segment_length samples."""
    out = []
    for level in levels:
        out.extend([float(level)] * segment_length)
    return out
