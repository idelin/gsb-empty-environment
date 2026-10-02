"""Local deterministic fixtures: sample arrays + sample rate.

No audio devices, no third-party deps; everything is generated with the
standard library and is bit-reproducible across runs.
"""
from __future__ import annotations

import math
import random
from typing import List, Sequence, Tuple

SAMPLE_RATE = 48000.0


def silence(duration_s: float, sample_rate: float = SAMPLE_RATE) -> List[float]:
    return [0.0] * int(round(duration_s * sample_rate))


def sine_wave(freq_hz: float = 440.0, duration_s: float = 0.25,
             amplitude: float = 0.8, phase: float = 0.0,
             sample_rate: float = SAMPLE_RATE) -> List[float]:
    n = int(round(duration_s * sample_rate))
    return [amplitude * math.sin(2.0 * math.pi * freq_hz * i / sample_rate + phase)
            for i in range(n)]


def step_signal(levels: Sequence[Tuple[float, float]],
                sample_rate: float = SAMPLE_RATE) -> List[float]:
    """(amplitude, duration_s) pairs concatenated into one signal."""
    out: List[float] = []
    for amp, dur in levels:
        out.extend([amp] * int(round(dur * sample_rate)))
    return out


def noise(duration_s: float, amplitude: float = 0.5, seed: int = 20261002,
          sample_rate: float = SAMPLE_RATE) -> List[float]:
    rng = random.Random(seed)
    n = int(round(duration_s * sample_rate))
    return [amplitude * (2.0 * rng.random() - 1.0) for _ in range(n)]


def ramp_up_down(duration_s: float = 0.4, peak: float = 0.9,
                 sample_rate: float = SAMPLE_RATE) -> List[float]:
    n = int(round(duration_s * sample_rate))
    half = n // 2
    return [peak * i / half for i in range(half)] + \
           [peak * (n - i) / (n - half) for i in range(half, n)]


def multichannel_sine(channels: int = 2, freq_hz: float = 440.0,
                      duration_s: float = 0.25,
                      sample_rate: float = SAMPLE_RATE) -> List[List[float]]:
    """Per-channel sines with distinct amplitudes/phases for sidechain tests."""
    return [sine_wave(freq_hz * (1 + ch), duration_s,
                     amplitude=0.9 / (ch + 1), phase=0.5 * ch,
                     sample_rate=sample_rate)
            for ch in range(channels)]


def demo_signal(sample_rate: float = SAMPLE_RATE) -> List[float]:
    """Silence -> quiet -> loud -> silence, exercising expander+compressor."""
    return (silence(0.02, sample_rate)
            + sine_wave(330.0, 0.10, 0.02, sample_rate=sample_rate)
            + sine_wave(440.0, 0.10, 0.9, sample_rate=sample_rate)
            + silence(0.02, sample_rate))
