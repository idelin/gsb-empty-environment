"""Local deterministic fixtures: sample arrays, sample rate, and the
declared latency / numeric tolerance of the offline scheduler.

No audio device is involved anywhere; all signal material comes from the
generators below.
"""

from __future__ import annotations

import math

SAMPLE_RATE = 48000
BLOCK_SIZE = 64

# Declared processing latency of the offline engine: one render block.
# There is no device latency because no audio device is used.
DECLARED_LATENCY_SAMPLES = BLOCK_SIZE

# Tolerance used when comparing floating-point output against the
# per-sample reference implementation.
NUMERIC_TOLERANCE = 1e-9

MASTER = "master"


def silence(n: int) -> list[float]:
    return [0.0] * n


def constant(n: int, value: float) -> list[float]:
    return [float(value)] * n


def impulse(n: int, index: int = 0, amplitude: float = 1.0) -> list[float]:
    out = [0.0] * n
    if 0 <= index < n:
        out[index] = float(amplitude)
    return out


def impulse_train(n: int, period: int, amplitude: float = 1.0) -> list[float]:
    out = [0.0] * n
    for i in range(0, n, period):
        out[i] = float(amplitude)
    return out


def sine(n: int, freq: float, sample_rate: int = SAMPLE_RATE,
         amplitude: float = 1.0, phase: float = 0.0) -> list[float]:
    return [amplitude * math.sin(2.0 * math.pi * freq * i / sample_rate
                                 + phase)
            for i in range(n)]
