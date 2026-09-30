"""Local fixture: signals, sample rate, fractional-delay coefficients, tolerances.

Everything is generated locally in pure Python; no audio device is involved.
The fractional-delay FIR coefficients are provided here (by the fixture) and
consumed verbatim by both the streaming engine and the reference convolution.
"""
from __future__ import annotations

import math

SAMPLE_RATE = 48_000.0   # Hz, declared sample rate of the fixture signals
KERNEL_LENGTH = 32       # fractional-delay FIR length (even)
MAX_DELAY = 64.0         # samples; upper bound for any declared delay
TOLERANCE = 1e-9         # declared numeric tolerance (engine vs reference)


def _sinc(x: float) -> float:
    if x == 0.0:
        return 1.0
    x *= math.pi
    return math.sin(x) / x


def _blackman(k: int, length: int) -> float:
    return (0.42
            - 0.5 * math.cos(2.0 * math.pi * k / (length - 1))
            + 0.08 * math.cos(4.0 * math.pi * k / (length - 1)))


def fractional_coefficients(mu: float) -> list[float]:
    """Fixture-provided fractional-delay FIR coefficients.

    Windowed-sinc interpolator, DC-normalized. ``mu`` is the fractional part
    of the read position in [0, 1). Tap ``k`` multiplies the input sample at
    index ``floor(p) - KERNEL_LENGTH // 2 + 1 + k``.
    """
    if not 0.0 <= mu < 1.0:
        raise ValueError(f"mu must be in [0, 1), got {mu!r}")
    length = KERNEL_LENGTH
    half = length // 2
    taps = [_sinc(mu + half - 1 - k) * _blackman(k, length) for k in range(length)]
    norm = sum(taps)
    return [tap / norm for tap in taps]


def impulse(length: int, index: int, amplitude: float = 1.0) -> list[float]:
    """Unit impulse at ``index`` inside a zero array of ``length`` samples."""
    samples = [0.0] * length
    samples[index] = float(amplitude)
    return samples


def sine(length: int, frequency: float, amplitude: float = 1.0,
         phase: float = 0.0) -> list[float]:
    """Real sine at ``frequency`` Hz sampled at SAMPLE_RATE."""
    return [
        amplitude * math.sin(2.0 * math.pi * frequency * n / SAMPLE_RATE + phase)
        for n in range(length)
    ]


def swept_sine(length: int, f0: float, f1: float,
               amplitude: float = 0.8) -> list[float]:
    """Linear chirp sweeping from ``f0`` to ``f1`` Hz over the whole array."""
    sweep_rate = (f1 - f0) / (length / SAMPLE_RATE)
    return [
        amplitude * math.sin(
            2.0 * math.pi * (f0 * t + 0.5 * sweep_rate * t * t)
        )
        for t in (n / SAMPLE_RATE for n in range(length))
    ]
